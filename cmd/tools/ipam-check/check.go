package main

import (
	"flag"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	k8sinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	networkv1alpha1 "github.com/yunify/hostnic-cni/pkg/apis/network/v1alpha1"
	clientset "github.com/yunify/hostnic-cni/pkg/client/clientset/versioned"
	informers "github.com/yunify/hostnic-cni/pkg/client/informers/externalversions"
	"github.com/yunify/hostnic-cni/pkg/constants"
	"github.com/yunify/hostnic-cni/pkg/networkutils"
	"github.com/yunify/hostnic-cni/pkg/rpc"
	"github.com/yunify/hostnic-cni/pkg/signals"
	"github.com/yunify/hostnic-cni/pkg/simple/client/network/ippool/ipam"
)

var kubeconfig string
var delete, debug bool

func usage() {
	fmt.Println("This tool is used to check leak ip arp rules, it will list all leak ip arp rules on this instance by default, if you want to delete them, please use '-delete' flag")
	flag.PrintDefaults()
}

func main() {
	flag.StringVar(&kubeconfig, "kubeconfig", "/root/.kube/config", "Path to a kubeconfig. Only required if out-of-cluster.If not set, use the default path")
	flag.BoolVar(&delete, "delete", false, "delete leak ip arp rules")
	flag.BoolVar(&debug, "debug", false, "show debug info")
	flag.Usage = usage
	flag.Parse()

	//get instance-id
	instanceIDByte, err := os.ReadFile(constants.InstanceIDFile)
	if err != nil {
		fmt.Printf("failed to load instance-id: %v", err)
		return
	}
	instanceID := strings.TrimSpace(string(instanceIDByte))
	fmt.Println("get instanceID: ", instanceID)

	// set up signals so we handle the first shutdown signals gracefully
	stopCh := signals.SetupSignalHandler()

	//client
	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		fmt.Printf("Error building kubeconfig: %v", err)
		return
	}

	k8sClient, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		fmt.Printf("Error building kubernetes clientset: %v", err)
		return
	}

	client, err := clientset.NewForConfig(cfg)
	if err != nil {
		fmt.Printf("Error building example clientset: %v", err)
		return
	}

	k8sInformerFactory := k8sinformers.NewSharedInformerFactory(k8sClient, time.Second*10)
	informerFactory := informers.NewSharedInformerFactory(client, time.Second*30)

	ipamClient := ipam.NewIPAMClient(client, networkv1alpha1.IPPoolTypeLocal, informerFactory, k8sInformerFactory)

	k8sInformerFactory.Start(stopCh)
	informerFactory.Start(stopCh)

	if err = ipamClient.Sync(stopCh); err != nil {
		fmt.Printf("ipamclient sync error: %v", err)
		return
	}

	//network tools for clean pod network
	networkutils.SetupNetworkHelper()

	//get all arp rules on the instance
	rules, err := getArpRuleList()
	if err != nil {
		fmt.Printf("getArpRuleList failed: %v\n", err)
		return
	}
	if debug {
		fmt.Printf("all arp rules on instance %s: \n", instanceID)
		for _, rule := range rules {
			fmt.Printf("\t%s\n", rule.rule)
		}
	}
	fmt.Printf("\n\n")

	//get pods on the instance
	pods, nodeName, err := ipamClient.ListInstancePods(instanceID)
	if err != nil {
		fmt.Printf("ListInstancePods failed: %v\n", err)
		return
	}

	podIPMap := make(map[string]*corev1.Pod)
	podKeyPrefixMap := make(map[string]*corev1.Pod)

	for _, pod := range pods {
		if pod.Status.PodIP != "" {
			podIPMap[pod.Status.PodIP] = pod
		}
		podKeyPrefixMap[pod.Namespace+"-"+pod.Name] = pod
	}

	//get handld id for this ip

	//check and gather leak ip arp rules
	rulesToClear := []arpReplyInfo{}
	for _, rule := range rules {
		var found bool
		if _, ok := podIPMap[rule.ip]; !ok {
			//should consider pod pending status
			handldID, _ := ipamClient.GetHandleIDForIP(rule.ip)
			if handldID != "" {
				// ip is allocated in ipamblock, check if the pod is on this instance
				for podKeyPrefix, pod := range podKeyPrefixMap {
					if strings.Contains(handldID, podKeyPrefix) && pod.Status.Phase == corev1.PodPending {
						// pod is on this instance
						found = true
						break
					}
				}
			}
			if found {
				continue
			}

			rulesToClear = append(rulesToClear, rule)
		}
	}

	if len(rulesToClear) == 0 {
		fmt.Printf("no leak ip arp rules found on instance %s(%s)\n\n", instanceID, nodeName)
		return
	}

	fmt.Printf("leak ip arp rules on instance %s(%s): \n", instanceID, nodeName)
	for _, rule := range rulesToClear {
		fmt.Printf("\t%s\n", rule.rule)
	}
	fmt.Printf("\n\n")

	if !delete {
		return
	}

	// delete leak ip arp rules and release ip
	// should delete arp rules first, then release ip, or if release ip error,the rule will left but ip was released and will be allocated to other pod
	fmt.Printf("going to delete leak ip arp rules\n\n")
	for _, rule := range rulesToClear {
		fmt.Printf("deleting leak ip arp rule: %s\n", rule.rule)
		if err := clearArpReplyRule(rule); err != nil {
			fmt.Printf("delete arp rule for leak ip %s failed: %v, skip\n\n", rule.ip, err)
			continue
		}
		fmt.Printf("delete arp rule for leak ip %s success\n\n", rule.ip)

		// TODO: should check if the ip is in use by other pods on other instances
		// update: only clear arp rules in this tools, release ip in ipam-client
		// if err := ipamClient.ReleaseByIP(rule.ip); err != nil {
		// 	fmt.Printf("release leak ip %s failed: %v, skip\n\n", rule.ip, err)
		// }
		// fmt.Printf("release leak ip %s success\n\n", rule.ip)
	}
}

// list arp rules
type arpReplyInfo struct {
	routeTableNum int32
	ip            string
	macAddr       string
	rule          string
}

func getArpRuleList() (ruleList []arpReplyInfo, err error) {
	//get all arp rules
	ruleStr, err := networkutils.ListArpReply()
	if err != nil {
		return nil, fmt.Errorf("list arp rules error: %v", err)
	}

	//parse arp rules
	return parseARPRules(ruleStr)
}

func parseARPRules(output string) (ruleList []arpReplyInfo, err error) {
	lines := strings.Split(output, "\n")

	ruleRegex := regexp.MustCompile(`-p ARP --logical-in (br_\d+) --arp-op Request --arp-ip-dst (\d+\.\d+\.\d+\.\d+) -j arpreply --arpreply-mac ([\da-fA-F:]+)`)

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "Bridge chain:") || strings.HasPrefix(line, "policy:") {
			// skip empty line and header
			continue
		}

		match := ruleRegex.FindStringSubmatch(line)
		if len(match) > 3 {
			logicalIn := match[1]
			ip := match[2]
			mac := match[3]

			tableNumStr := strings.TrimPrefix(logicalIn, "br_")
			tableNum, err := strconv.Atoi(tableNumStr)
			if err != nil {
				return nil, fmt.Errorf("parse tableNumStr %s error: %v", tableNumStr, err)
			}

			ruleList = append(ruleList, arpReplyInfo{
				routeTableNum: int32(tableNum),
				ip:            ip,
				macAddr:       mac,
				rule:          line,
			})
		}
	}
	return
}

// check and delete arp rules
func clearArpReplyRule(item arpReplyInfo) error {
	//delete arp rule
	err := networkutils.NetworkHelper.CleanupPodNetwork(&rpc.HostNic{
		RouteTableNum: item.routeTableNum,
		HardwareAddr:  item.macAddr,
	}, item.ip)
	if err != nil && !strings.Contains(err.Error(), "rule does not exist") {
		return fmt.Errorf("failed to delete ebtables rule %s: %s", item.rule, err)
	}

	return nil
}
