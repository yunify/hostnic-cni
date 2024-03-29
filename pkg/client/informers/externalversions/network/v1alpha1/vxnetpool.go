/*
Copyright 2020 The KubeSphere Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Code generated by informer-gen. DO NOT EDIT.

package v1alpha1

import (
	"context"
	time "time"

	networkv1alpha1 "github.com/yunify/hostnic-cni/pkg/apis/network/v1alpha1"
	versioned "github.com/yunify/hostnic-cni/pkg/client/clientset/versioned"
	internalinterfaces "github.com/yunify/hostnic-cni/pkg/client/informers/externalversions/internalinterfaces"
	v1alpha1 "github.com/yunify/hostnic-cni/pkg/client/listers/network/v1alpha1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
	watch "k8s.io/apimachinery/pkg/watch"
	cache "k8s.io/client-go/tools/cache"
)

// VxNetPoolInformer provides access to a shared informer and lister for
// VxNetPools.
type VxNetPoolInformer interface {
	Informer() cache.SharedIndexInformer
	Lister() v1alpha1.VxNetPoolLister
}

type vxNetPoolInformer struct {
	factory          internalinterfaces.SharedInformerFactory
	tweakListOptions internalinterfaces.TweakListOptionsFunc
}

// NewVxNetPoolInformer constructs a new informer for VxNetPool type.
// Always prefer using an informer factory to get a shared informer instead of getting an independent
// one. This reduces memory footprint and number of connections to the server.
func NewVxNetPoolInformer(client versioned.Interface, resyncPeriod time.Duration, indexers cache.Indexers) cache.SharedIndexInformer {
	return NewFilteredVxNetPoolInformer(client, resyncPeriod, indexers, nil)
}

// NewFilteredVxNetPoolInformer constructs a new informer for VxNetPool type.
// Always prefer using an informer factory to get a shared informer instead of getting an independent
// one. This reduces memory footprint and number of connections to the server.
func NewFilteredVxNetPoolInformer(client versioned.Interface, resyncPeriod time.Duration, indexers cache.Indexers, tweakListOptions internalinterfaces.TweakListOptionsFunc) cache.SharedIndexInformer {
	return cache.NewSharedIndexInformer(
		&cache.ListWatch{
			ListFunc: func(options v1.ListOptions) (runtime.Object, error) {
				if tweakListOptions != nil {
					tweakListOptions(&options)
				}
				return client.NetworkV1alpha1().VxNetPools().List(context.TODO(), options)
			},
			WatchFunc: func(options v1.ListOptions) (watch.Interface, error) {
				if tweakListOptions != nil {
					tweakListOptions(&options)
				}
				return client.NetworkV1alpha1().VxNetPools().Watch(context.TODO(), options)
			},
		},
		&networkv1alpha1.VxNetPool{},
		resyncPeriod,
		indexers,
	)
}

func (f *vxNetPoolInformer) defaultInformer(client versioned.Interface, resyncPeriod time.Duration) cache.SharedIndexInformer {
	return NewFilteredVxNetPoolInformer(client, resyncPeriod, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc}, f.tweakListOptions)
}

func (f *vxNetPoolInformer) Informer() cache.SharedIndexInformer {
	return f.factory.InformerFor(&networkv1alpha1.VxNetPool{}, f.defaultInformer)
}

func (f *vxNetPoolInformer) Lister() v1alpha1.VxNetPoolLister {
	return v1alpha1.NewVxNetPoolLister(f.Informer().GetIndexer())
}
