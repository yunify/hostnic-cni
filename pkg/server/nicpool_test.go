package server

import (
	"github.com/yunify/hostnic-cni/pkg"
	log "github.com/sirupsen/logrus"
	"sync"
	"testing"
	"strconv"
	"time"
	"math/rand"
	"github.com/orcaman/concurrent-map"
)

type MockProviderInterface interface {
	CloseAndCheck()bool
}

type MockProvider struct {
	index int
	sync.Mutex
	result cmap.ConcurrentMap
}

func (provider *MockProvider) CloseAndCheck() bool {
	return len(provider.result.Items()) == 0
}

func NewMockProvider() *MockProvider {
	return &MockProvider{index:0,result:cmap.New()}
}

func (provider *MockProvider) GenerateNic() (*pkg.HostNic, error) {
	provider.Lock()
	defer provider.Unlock()
	id := strconv.Itoa(provider.index)
	provider.result.Set(id,true)
	provider.index += 1
	return &pkg.HostNic{
		ID: id,
	},nil

}

func (provider *MockProvider) ValidateNic(nicid string) bool {
	rand.Seed(time.Now().UnixNano())
	result := rand.Int() % 10 != 1
	if !result{
		provider.result.Remove(nicid)
	}
	return result
}

func (provider *MockProvider) ReclaimNic(ids[]*string) error {
	for _,id := range ids{
		provider.result.Remove(*id)
	}
	return nil
}

func (provider *MockProvider) GetNicsInfo(idlist []*string) ([]*pkg.HostNic, error) {
	var result []*pkg.HostNic
	for _,item := range idlist {
		result = append(result,&pkg.HostNic{ID:*item})
	}
	return result,nil
}




func TestPoolRace(t *testing.T){
	t.Run("group", func(t *testing.T) {
		t.Parallel()
		log.SetLevel(log.DebugLevel)
		provider := NewMockProvider()
		nicpool,err:=NewNicPool(3,provider,nil,NicPoolConfig{true})
		if err != nil {
			t.Fatal(err)
		}
		allocateReclaimTest := func(t *testing.T) {
			nic,err:=nicpool.BorrowNic(false)
			if err!= nil {
				t.Fatal(err)
			}

			err = nicpool.ReturnNic(nic.ID)
			if err!= nil {
				t.Fatal(err)
			}
		}
		for i:=0 ;i < 64 ;i ++ {
			t.Run("Allocate and Reclaim", allocateReclaimTest)
		}
		nicpool.ShutdownNicPool()
		ok:=provider.CloseAndCheck()
		if !ok {
			t.Fatal("Provider check failed")
		}
	})
}


