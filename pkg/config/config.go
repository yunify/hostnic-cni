package config

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sync"

	"github.com/yunify/hostnic-cni/pkg/constants"

	corev1 "k8s.io/api/core/v1"
	v1Informers "k8s.io/client-go/informers/core/v1"
	v1Listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
)

/* example:
data:
  ipam: |
    {
      "Default": ["vxnet-cwjk6xr","vxnet-kuusp12"],
      "test": ["4100-172-16-3-0-26", "4100-172-16-3-128-26"],
      "abc": ["4100-172-16-3-64-26", "4100-172-16-3-192-26"]
    }
*/

type ClusterConfig struct {
	configMapSynced   cache.InformerSynced
	configMapLister   v1Listers.ConfigMapLister
	configMapInformer cache.SharedIndexInformer

	lock       *sync.RWMutex
	apps       map[string][]string
	autoAssign bool
}

func NewClusterConfig(configMapInformer v1Informers.ConfigMapInformer) *ClusterConfig {
	c := &ClusterConfig{
		configMapSynced:   configMapInformer.Informer().HasSynced,
		configMapLister:   configMapInformer.Lister(),
		configMapInformer: configMapInformer.Informer(),
		lock:              &sync.RWMutex{},
	}

	configMapInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			c.configHandle(obj.(*corev1.ConfigMap), constants.EventADD)
		},
		UpdateFunc: func(old, new interface{}) {
			newconf := new.(*corev1.ConfigMap)
			oldConf := old.(*corev1.ConfigMap)
			if !reflect.DeepEqual(newconf.Data, oldConf.Data) {
				c.configHandle(newconf, constants.EventUpdate)
			}
		},
		DeleteFunc: func(obj interface{}) {
			c.configHandle(obj.(*corev1.ConfigMap), constants.EventDelete)
		},
	})

	return c
}

func (c *ClusterConfig) Sync(stopCh <-chan struct{}) error {
	klog.Info("Waiting for configmap caches to sync")
	if ok := cache.WaitForCacheSync(stopCh, c.configMapSynced); !ok {
		return fmt.Errorf("failed to wait for configmap caches to sync")
	}
	return nil
}

func (c *ClusterConfig) configHandle(cm *corev1.ConfigMap, event string) {
	c.lock.Lock()
	defer c.lock.Unlock()

	if cm.Namespace == constants.IPAMConfigNamespace && cm.Name == constants.IPAMConfigName {
		if event == constants.EventDelete || cm.DeletionTimestamp != nil {
			c.apps = nil
			c.autoAssign = false
			return
		}

		var apps map[string][]string
		if err := json.Unmarshal([]byte(cm.Data[constants.IPAMConfigDate]), &apps); err == nil {
			c.apps = apps
		} else {
			klog.Errorf("Get configmap %s/%s failed: %v", constants.IPAMConfigNamespace, constants.IPAMConfigName, err)
		}

		if cm.Data[constants.IPAMAutoAssignForNamespace] == "on" {
			c.autoAssign = true
		} else {
			c.autoAssign = false
		}
	}
}

// TODO: delete later
func (c *ClusterConfig) GetConfig() map[string][]string {
	c.lock.RLock()
	defer c.lock.RUnlock()

	return c.apps
}

func (c *ClusterConfig) GetDefaultIPPools() []string {
	c.lock.RLock()
	defer c.lock.RUnlock()

	if c.autoAssign {
		return nil
	}

	rst := make([]string, len(c.apps[constants.IPAMDefaultPoolKey]))
	copy(rst, c.apps[constants.IPAMDefaultPoolKey])
	return rst
}

func (c *ClusterConfig) GetBlocksForAPP(app string) []string {
	c.lock.RLock()
	defer c.lock.RUnlock()

	rst := make([]string, len(c.apps[app]))
	copy(rst, c.apps[app])
	return rst
}
