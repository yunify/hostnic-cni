package provider

import "fmt"

var initializerMap = make(map[string]Initializer)

// Register register new provider
func Register(name string, init Initializer) {
	initializerMap[name] = init
}

//New create new nic provider from config
func New(name string, conf map[string]interface{}) (NicProvider, error) {
	if init := initializerMap[name]; init != nil {
		return init(conf)
	}
	return nil, fmt.Errorf("Unsupported provider: %s", name)
}
