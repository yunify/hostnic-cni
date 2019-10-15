package ipam

import (
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/yunify/hostnic-cni/pkg/ipam/datastore"
	"github.com/yunify/hostnic-cni/pkg/k8sclient"
	"github.com/yunify/hostnic-cni/pkg/networkutils"
	"github.com/yunify/hostnic-cni/pkg/qcclient"
	"k8s.io/client-go/kubernetes"
)

func TestIpam(t *testing.T) {
	// klog.InitFlags(nil)
	// flag.Set("logtostderr", "true")
	// flag.Set("v", "4")
	// flag.Parse()
	// klog.SetOutput(GinkgoWriter)
	RegisterFailHandler(Fail)
	RunSpecs(t, "Ipam Suite")
}

func NewFakeIPAM(netapi networkutils.NetworkAPIs, clientset kubernetes.Interface, prepareCloud func(*qcclient.LabelResourceConfig) (qcclient.QingCloudAPI, error)) *IpamD {
	return &IpamD{
		dataStore:          datastore.NewDataStore(),
		networkClient:      netapi,
		poolSize:           defaultPoolSize,
		maxPoolSize:        defaultMaxPoolSize,
		K8sClient:          k8sclient.NewK8sHelper(clientset),
		prepareCloudClient: prepareCloud,
	}
}
