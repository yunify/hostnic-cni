package k8s

import (
	"os"

	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

const (
	// NodeNameEnvKey is env to get the name of current node
	NodeNameEnvKey   = "MY_NODE_NAME"
	AnnoHostNicVxnet = "hostnic.network.kubesphere.io/vxnet"
	AnnoHostNic      = "hostnic.network.kubesphere.io/nic"
	AnnoHostNicIP    = "hostnic.network.kubesphere.io/ip"
	AnnoHostNicType  = "hostnic.network.kubesphere.io/type"
)

type Helper struct {
	NodeName string
	Client   client.Client

	PodEvent record.EventRecorder
	Mgr      manager.Manager
}

var (
	scheme    = runtime.NewScheme()
	K8sHelper *Helper
)

func init() {
	_ = corev1.AddToScheme(scheme)
}

func SetupK8sHelper() {
	config, err := rest.InClusterConfig()
	if err != nil {
		log.WithError(err).Fatalf("failed to get k8s config")
	}

	mgr, err := manager.New(config, manager.Options{Scheme: scheme})
	if err != nil {
		log.WithError(err).Fatalf("failed to new k8s manager")
	}

	nodeName := os.Getenv(NodeNameEnvKey)
	if nodeName == "" {
		log.Fatalf("node name should not be empty")
	}

	K8sHelper = &Helper{
		NodeName: nodeName,
		Client:   mgr.GetClient(),
		PodEvent: mgr.GetEventRecorderFor("hostnic"),
		Mgr:      mgr,
	}
}
