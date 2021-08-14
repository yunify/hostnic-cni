package main

import (
	"encoding/json"
	"fmt"

	v1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"

	"github.com/yunify/hostnic-cni/pkg/constants"
)

// check ipam-configmap
func admitConfigMaps(ar v1.AdmissionReview) *v1.AdmissionResponse {
	configMapResource := metav1.GroupVersionResource{Group: "", Version: "v1", Resource: "configmaps"}
	if ar.Request.Resource != configMapResource {
		klog.Errorf("expect resource to be %s", configMapResource)
		return nil
	}

	var raw []byte
	if ar.Request.Operation == v1.Delete {
		raw = ar.Request.OldObject.Raw
	} else {
		raw = ar.Request.Object.Raw
	}
	configmap := corev1.ConfigMap{}
	deserializer := codecs.UniversalDeserializer()
	if _, _, err := deserializer.Decode(raw, nil, &configmap); err != nil {
		klog.Error(err)
		return &v1.AdmissionResponse{
			Result: &metav1.Status{
				Message: err.Error(),
			},
		}
	}

	reviewResponse := v1.AdmissionResponse{}
	reviewResponse.Allowed = true
	if configmap.Namespace != constants.IPAMConfigNamespace || configmap.Name != constants.IPAMConfigName {
		return &reviewResponse
	}

	// 1. check configmap format
	var apps map[string][]string
	if err := json.Unmarshal([]byte(configmap.Data[constants.IPAMConfigDate]), &apps); err != nil {
		err = fmt.Errorf("ipam format error: %v", err)
		klog.Error(err)
		reviewResponse.Allowed = false
		reviewResponse.Result = &metav1.Status{
			Reason: metav1.StatusReason(err.Error()),
		}
		return &reviewResponse
	}

	// 2. check assignment:
	//     a)namespace could have one or more subnets
	//     b)but one subnet could only assigned to only one namespace
	assignment := make(map[string]string)
	for namespace, subnets := range apps {
		for _, subnet := range subnets {
			if ns, ok := assignment[subnet]; ok && ns != namespace {
				err := fmt.Errorf("subnet %s was assigned to namespaces (%s %s) which was not allowed", subnet, ns, namespace)
				klog.Error(err)
				reviewResponse.Allowed = false
				reviewResponse.Result = &metav1.Status{
					Reason: metav1.StatusReason(err.Error()),
				}
				return &reviewResponse
			}
			assignment[subnet] = namespace
		}
	}

	return &reviewResponse
}
