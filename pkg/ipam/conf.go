package ipam

import (
	"os"
	"strings"
	"text/template"
)

func (s *IpamD) WriteCNIConfig() error {
	f, err := os.Create(configFileName)
	if err != nil {
		return err
	}
	defer f.Close()
	var conf struct {
		CniVersion string `json:"cniVersion"`
		VethPrefix string `json:"vethPrefix,omitempty"`
	}
	conf.CniVersion = "0.3.1"
	//TODO can be user defined
	conf.VethPrefix = s.vethPrefix
	templ :=
		`{
	"cniVersion": "{{.CniVersion}}",
	"name": "hostnic-cni",
	"plugins": [
		{
		"name": "hostnic",
		"type": "hostnic",
		"vethPrefix": "{{.VethPrefix}}"
		}]
}`
	t, err := template.New("cni-config").Parse(templ)
	if err != nil {
		return err
	}
	return t.Execute(f, &conf)
}

func (s *IpamD) parseEnv() {
	t := os.Getenv(envExtraTags)
	if t != "" {
		s.extraTags = strings.Split(t, ",")
	}
	s.clusterName = os.Getenv(envClusterName)
	if s.clusterName == "" {
		s.clusterName = defaultClusterName
	}
	s.vethPrefix = os.Getenv(envVethPrefix)
	if s.vethPrefix == "" {
		s.vethPrefix = defaultVethPrefix
	}
}
