module github.com/yunify/hostnic-cni

go 1.13

require (
	github.com/containernetworking/cni v0.8.0
	github.com/containernetworking/plugins v0.8.6
	github.com/davecgh/go-spew v1.1.1
	github.com/golang/protobuf v1.4.3
	github.com/kelseyhightower/envconfig v1.4.0 // indirect
	github.com/magiconair/properties v1.8.4 // indirect
	github.com/mitchellh/mapstructure v1.4.0 // indirect
	github.com/pelletier/go-toml v1.8.1 // indirect
	github.com/pilebones/go-udev v0.0.0-20180820235104-043677e09b13
	github.com/pkg/errors v0.8.1
	github.com/projectcalico/libcalico-go v1.7.2-0.20201119205058-b367043ede58
	github.com/sirupsen/logrus v1.6.0
	github.com/spf13/afero v1.5.1 // indirect
	github.com/spf13/cast v1.3.1 // indirect
	github.com/spf13/jwalterweatherman v1.1.0 // indirect
	github.com/spf13/viper v1.7.1
	github.com/syndtr/goleveldb v1.0.0
	github.com/vishvananda/netlink v1.1.0
	github.com/yunify/qingcloud-sdk-go v0.0.0-20201229081442-29b014374d9d
	golang.org/x/sys v0.0.0-20201214210602-f9fddec55a1e
	golang.org/x/text v0.3.4 // indirect
	google.golang.org/grpc v1.27.0
	google.golang.org/protobuf v1.25.0 // indirect
	gopkg.in/ini.v1 v1.62.0 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
	k8s.io/api v0.18.6
	k8s.io/apimachinery v0.18.6
	k8s.io/client-go v0.18.6
	k8s.io/utils v0.0.0-20200619165400-6e3d28b6ed19 // indirect
	sigs.k8s.io/controller-runtime v0.6.4
)

replace github.com/DATA-DOG/godog v0.10.0 => github.com/cucumber/godog v0.7.9
