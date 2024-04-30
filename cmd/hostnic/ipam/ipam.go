//
// =========================================================================
// Copyright (C) 2017 by Yunify, Inc...
// -------------------------------------------------------------------------
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this work except in compliance with the License.
// You may obtain a copy of the License in the LICENSE file, or at:
//
//  http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
// =========================================================================
//

package ipam

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/containernetworking/cni/pkg/skel"
	"github.com/containernetworking/cni/pkg/types"
	"github.com/containernetworking/cni/pkg/types/current"
	"github.com/davecgh/go-spew/spew"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"

	. "github.com/yunify/hostnic-cni/pkg/constants"
	"github.com/yunify/hostnic-cni/pkg/networkutils"
	"github.com/yunify/hostnic-cni/pkg/rpc"
)

func AddrAlloc(args *skel.CmdArgs) (*rpc.IPAMMessage, *current.Result, error) {
	conf := NetConf{}
	if err := json.Unmarshal(args.StdinData, &conf); err != nil {
		return nil, nil, fmt.Errorf("failed to unmarshal netconf %s", spew.Sdump(args))
	}

	k8sArgs := K8sArgs{}
	if err := types.LoadArgs(args.Args, &k8sArgs); err != nil {
		return nil, nil, fmt.Errorf("failed to load k8s args  %s", spew.Sdump(args))
	}

	// Set up a connection to the ipamD server.
	conn, err := grpc.Dial(DefaultUnixSocketPath, grpc.WithInsecure())
	if err != nil {
		return nil, nil, fmt.Errorf("failed to connect server, err=%v", err)
	}
	defer conn.Close()

	c := rpc.NewCNIBackendClient(conn)
	r, err := c.AddNetwork(context.Background(),
		&rpc.IPAMMessage{
			Args: &rpc.PodInfo{
				Name:       string(k8sArgs.K8S_POD_NAME),
				Namespace:  string(k8sArgs.K8S_POD_NAMESPACE),
				Containter: string(k8sArgs.K8S_POD_INFRA_CONTAINER_ID),
				Netns:      args.Netns,
				IfName:     args.IfName,
			},
		})
	if err != nil {
		return nil, nil, err
	}

	nic := r.Nic

	//wait for nic attach
	for {
		link, err := networkutils.NetworkHelper.LinkByMacAddr(nic.HardwareAddr)
		if err != nil && err != ErrNicNotFound {
			return nil, nil, err
		}
		if link != nil {
			break
		}
		time.Sleep(1 * time.Second)
	}

	index := 0
	result := &current.Result{
		IPs: []*current.IPConfig{
			{
				Version: "4",
				Address: net.IPNet{
					IP:   net.ParseIP(r.IP),
					Mask: net.CIDRMask(32, 32),
				},
				Interface: &index,
				Gateway:   net.ParseIP("169.254.1.1"),
			},
		},
	}

	return r, result, nil
}

func AddrUnalloc(args *skel.CmdArgs, peek bool) (*rpc.IPAMMessage, error) {
	conf := NetConf{}
	if err := json.Unmarshal(args.StdinData, &conf); err != nil {
		return nil, fmt.Errorf("failed to unmarshal netconf %s", spew.Sdump(args))
	}

	k8sArgs := K8sArgs{}
	if err := types.LoadArgs(args.Args, &k8sArgs); err != nil {
		return nil, fmt.Errorf("failed to load k8s args  %s", spew.Sdump(args))
	}

	info := &rpc.IPAMMessage{
		Args: &rpc.PodInfo{
			Name:       string(k8sArgs.K8S_POD_NAME),
			Namespace:  string(k8sArgs.K8S_POD_NAMESPACE),
			Containter: string(k8sArgs.K8S_POD_INFRA_CONTAINER_ID),
			Netns:      args.Netns,
			IfName:     args.IfName,
		},
		Peek: peek,
	}

	// notify local PodIP address manager to free secondary PodIP
	// Set up a connection to the server.
	conn, err := grpc.Dial(DefaultUnixSocketPath, grpc.WithInsecure(), grpc.WithConnectParams(grpc.ConnectParams{
		Backoff:           backoff.DefaultConfig,
		MinConnectTimeout: 30 * time.Second,
	}))
	if err != nil {
		return nil, fmt.Errorf("failed to connect server")
	}
	defer conn.Close()

	c := rpc.NewCNIBackendClient(conn)
	reply, err := c.DelNetwork(context.Background(), info)
	if err != nil {
		return nil, fmt.Errorf("failed to call DelNetwork")
	}
	if reply.Nic == nil || reply.Args.Containter != args.ContainerID {
		return nil, ErrNicNotFound
	} else {
		if err := networkutils.NetworkHelper.CleanupPodNetwork(reply.Nic, reply.IP); err != nil {
			logrus.Errorf("clean %v %s network failed: %v", reply.Nic, reply.IP, err)
			//HOSTNIC_TODO: try to cleanup ebtables rules once there
		}
	}

	return reply, nil
}
