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
	"fmt"
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
		rand.Seed(time.Now().UnixNano())
		if rand.Intn(2) == 1 {
			result = append(result, &pkg.HostNic{ID: *item})
			provider.result.Set(*item, true)
		} else {
			return nil,fmt.Errorf("nic %s not found",*item)
		}
	}
	return result,nil
}




func TestPoolRace(t *testing.T){
	log.SetLevel(log.DebugLevel)
	provider := NewMockProvider()
	nicpool,err:=NewNicPool(3,provider,nil,NicPoolConfig{true})
	if err != nil {
		t.Fatal(err)
	}
	t.Run("group", func(t *testing.T) {

		allocateReclaimTest := func(t *testing.T) {
			t.Parallel()
			var err error

			nic,err:=nicpool.BorrowNic(false)
			if err!= nil {
				t.Fatal(err)
				return
			}
			if nic == nil {
				t.Fatal("Nic is nil")
				return
			}

			err = nicpool.ReturnNic(nic.ID)
			if err!= nil {
				t.Fatal(err)
				return
			}
		}
		returnRecoverTest := func(t *testing.T) {
			t.Parallel()
			rand.Seed(time.Now().UnixNano())
			result := 64+rand.Int()
			nicid := strconv.Itoa(result)
			nicpool.ReturnNic(nicid)
			return
		}
		for i:=0 ;i < 64 ;i ++ {
			t.Run("Allocate and Reclaim", allocateReclaimTest)
			if i %10 ==0 {
				t.Run("Return nic from old daemon", returnRecoverTest)
			}
		}

	})
	nicpool.ShutdownNicPool()
	ok:=provider.CloseAndCheck()
	if !ok {
		t.Fatal("Provider check failed")
	}
}


