package main

import (
	"github.com/jolestar/go-commons-pool"
	"github.com/yunify/hostnic-cni/provider"
	"github.com/yunify/hostnic-cni/pkg"
)

type NicFactory struct {
	provider provider.NicProvider
	instanceID string
}

func (f *NicFactory) MakeObject() (*pool.PooledObject, error) {
	nic, err := f.provider.CreateNic(f.instanceID)
	if err != nil {
		return nil, err
	}
	return pool.NewPooledObject(nic), nil
}

func (f *NicFactory) DestroyObject(object *pool.PooledObject) error {
	hostNic := object.Object.(*pkg.HostNic)
	err := f.provider.DeleteNic(hostNic)
	return err
}

func (f *NicFactory) ValidateObject(object *pool.PooledObject) bool {
	//do validate
	return true
}

func (f *NicFactory) ActivateObject(object *pool.PooledObject) error {
	//do activate
	return nil
}

func (f *NicFactory) PassivateObject(object *pool.PooledObject) error {
	//do passivate
	return nil
}

func NewNicPool(provider provider.NicProvider, instanceID string) *pool.ObjectPool{
	f := &NicFactory{provider:provider, instanceID:instanceID}
	p := pool.NewObjectPoolWithDefaultConfig(f)
	return p
}