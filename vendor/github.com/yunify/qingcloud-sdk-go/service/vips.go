// +-------------------------------------------------------------------------
// | Copyright (C) 2016 Yunify, Inc.
// +-------------------------------------------------------------------------
// | Licensed under the Apache License, Version 2.0 (the "License");
// | you may not use this work except in compliance with the License.
// | You may obtain a copy of the License in the LICENSE file, or at:
// |
// | http://www.apache.org/licenses/LICENSE-2.0
// |
// | Unless required by applicable law or agreed to in writing, software
// | distributed under the License is distributed on an "AS IS" BASIS,
// | WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// | See the License for the specific language governing permissions and
// | limitations under the License.
// +-------------------------------------------------------------------------

package service

import (
	"fmt"
	"time"

	"github.com/yunify/qingcloud-sdk-go/config"
	"github.com/yunify/qingcloud-sdk-go/request"
	"github.com/yunify/qingcloud-sdk-go/request/data"
	"github.com/yunify/qingcloud-sdk-go/request/errors"
)

var _ fmt.State
var _ time.Time

type VIPService struct {
	Config     *config.Config
	Properties *VIPServiceProperties
}

type VIPServiceProperties struct {
	// QingCloud Zone ID
	Zone *string `json:"zone" name:"zone"` // Required
}

func (s *QingCloudService) VIP(zone string) (*VIPService, error) {
	properties := &VIPServiceProperties{
		Zone: &zone,
	}

	return &VIPService{Config: s.Config, Properties: properties}, nil
}

func (s *VIPService) CreateVIPs(i *CreateVIPsInput) (*CreateVIPsOutput, error) {
	if i == nil {
		i = &CreateVIPsInput{}
	}
	o := &data.Operation{
		Config:        s.Config,
		Properties:    s.Properties,
		APIName:       "AllocateVips",
		RequestMethod: "GET",
	}

	x := &CreateVIPsOutput{}
	r, err := request.New(o, i, x)
	if err != nil {
		return nil, err
	}

	err = r.Send()
	if err != nil {
		return nil, err
	}

	return x, err
}

type CreateVIPsInput struct {
	Count    *int    `json:"count" name:"count" default:"1" location:"params"`
	VIPName  *string `json:"vip_name" name:"vip_name" location:"params"`
	VIPRange *string `json:"vip_range" name:"vip_range" location:"params"`
	VxNetID  *string `json:"vxnet_id" name:"vxnet_id" location:"params"` // Required
}

func (v *CreateVIPsInput) Validate() error {
	// TODO
	if v.VxNetID == nil {
		return errors.ParameterRequiredError{
			ParameterName: "VxNetID",
			ParentName:    "CreateVIPsInput",
		}
	}
	return nil
}

type CreateVIPsOutput struct {
	Message *string   `json:"message" name:"message"`
	Action  *string   `json:"action" name:"action" location:"elements"`
	RetCode *int      `json:"ret_code" name:"ret_code" location:"elements"`
	JobID   *string   `json:"job_id" name:"job_id" location:"elements"`
	VIPs    []*string `json:"vips" name:"vips" location:"elements"`
}

func (s *VIPService) DeleteVIPs(i *DeleteVIPsInput) (*DeleteVIPsOutput, error) {
	if i == nil {
		i = &DeleteVIPsInput{}
	}
	o := &data.Operation{
		Config:        s.Config,
		Properties:    s.Properties,
		APIName:       "ReleaseVips",
		RequestMethod: "GET",
	}

	x := &DeleteVIPsOutput{}
	r, err := request.New(o, i, x)
	if err != nil {
		return nil, err
	}

	err = r.Send()
	if err != nil {
		return nil, err
	}

	return x, err
}

type DeleteVIPsInput struct {
	VIPs []*string `json:"vips" name:"vips" location:"params"` // Required
}

func (v *DeleteVIPsInput) Validate() error {

	if len(v.VIPs) == 0 {
		return errors.ParameterRequiredError{
			ParameterName: "vips",
			ParentName:    "DeleteVIPsInput",
		}
	}

	return nil
}

type DeleteVIPsOutput struct {
	Message *string `json:"message" name:"message"`
	Action  *string `json:"action" name:"action" location:"elements"`
	RetCode *int    `json:"ret_code" name:"ret_code" location:"elements"`
	JobID   *string `json:"job_id" name:"job_id" location:"elements"`
}

func (s *VIPService) DescribeVxNetsVIPs(i *DescribeVxNetsVIPsInput) (*DescribeVxNetsVIPsOutput, error) {
	if i == nil {
		i = &DescribeVxNetsVIPsInput{}
	}
	o := &data.Operation{
		Config:        s.Config,
		Properties:    s.Properties,
		APIName:       "DescribeVips",
		RequestMethod: "GET",
	}

	x := &DescribeVxNetsVIPsOutput{}
	r, err := request.New(o, i, x)
	if err != nil {
		return nil, err
	}

	err = r.Send()
	if err != nil {
		return nil, err
	}

	return x, err
}

type DescribeVxNetsVIPsInput struct {
	Limit   *int      `json:"limit" name:"limit" default:"20" location:"params"`
	Offset  *int      `json:"offset" name:"offset" default:"0" location:"params"`
	VIPName *string   `json:"vip_name" name:"vip_name" location:"params"`
	VxNets  []*string `json:"vxnets" name:"vxnets" location:"elements"` // Required
}

func (v *DescribeVxNetsVIPsInput) Validate() error {

	if len(v.VxNets) == 0 {
		return errors.ParameterRequiredError{
			ParameterName: "VxNets",
			ParentName:    "DescribeVxNetsVIPsInput",
		}
	}

	return nil
}

type DescribeVxNetsVIPsOutput struct {
	Message    *string `json:"message" name:"message"`
	Action     *string `json:"action" name:"action" location:"elements"`
	VIPSet     []*VIP  `json:"vip_set" name:"vip_set" location:"elements"`
	RetCode    *int    `json:"ret_code" name:"ret_code" location:"elements"`
	TotalCount *int    `json:"total_count" name:"total_count" location:"elements"`
}
