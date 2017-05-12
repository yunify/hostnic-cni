package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/containernetworking/cni/pkg/types"
	"github.com/yunify/hostnic-cni/pkg"
)

const defaultDataDir = "/var/lib/cni/hostnic"

//Config nic plugin configuration
type Config struct {
	types.NetConf
	Provider string                 `json:"provider"`
	DataDir  string                 `json:"dataDir"`
	Config   map[string]interface{} `json:"args"`
}

func loadNetConf(bytes []byte) (*Config, error) {
	netconf := &Config{DataDir: defaultDataDir}
	if err := json.Unmarshal(bytes, netconf); err != nil {
		return nil, fmt.Errorf("failed to parse netconf: %v", err)
	}
	if netconf.Provider == "" {
		return nil, errors.New("provider is empty")
	}

	return netconf, nil
}

func saveScratchNetConf(containerID, dataDir string, nic *pkg.HostNic) error {
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return fmt.Errorf("failed to create the multus data directory(%q): %v", dataDir, err)
	}

	path := filepath.Join(dataDir, containerID)
	data, err := json.Marshal(nic)
	if err != nil {
		return fmt.Errorf("failed to marshal nic %++v : %v", *nic, err)
	}
	err = ioutil.WriteFile(path, data, 0600)
	if err != nil {
		return fmt.Errorf("failed to write container data in the path(%q): %v", path, err)
	}

	return err
}

func consumeScratchNetConf(containerID, dataDir string) (*pkg.HostNic, error) {
	path := filepath.Join(dataDir, containerID)
	defer os.Remove(path)

	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read container data in the path(%q): %v", path, err)
	}
	hostNic := &pkg.HostNic{}
	err = json.Unmarshal(data, hostNic)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal nic data in the path(%q): %v", path, err)
	}
	return hostNic, err
}
