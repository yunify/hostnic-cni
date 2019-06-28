package k8sclient

import (
	"log"
	"os"
	"strings"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/kubernetes/fake"
)

var _ = Describe("K8sclient", func() {
	var nodeName string
	var stopCh chan struct{}
	var podTemplate string
	BeforeEach(func() {
		//prepare env
		nodeName = "testNode"
		os.Setenv(NodeNameEnvKey, nodeName)
		stopCh = make(chan struct{})

		podTemplate = `
		{
			"apiVersion": "v1",
			"kind": "Pod",
			"metadata": {
				"creationTimestamp": "2019-03-13T03:07:11Z",
				"generateName": "deck-7cd876b8d7-",
				"labels": {
					"app": "deck",
					"pod-template-hash": "7cd876b8d7"
				},
				"name": "template",
				"namespace": "default",
				"ownerReferences": [
					{
						"apiVersion": "apps/v1",
						"blockOwnerDeletion": true,
						"controller": true,
						"kind": "ReplicaSet",
						"name": "templateDeploy",
						"uid": "175e314d-453d-11e9-b860-5254d28e2fa4"
					}
				],
				"selfLink": "/api/v1/namespaces/default/pods/deck-7cd876b8d7-q7hhj",
				"uid": "18317e3f-453d-11e9-b860-5254d28e2fa4"
			},
			"spec": {
				"containers": [
					{
						"args": [
							"--tide-url=http://tide/",
							"--hook-url=http://hook:8888/plugin-help"
						],
						"image": "gcr.io/k8s-prow/deck:v20190312-abfe0e0",
						"imagePullPolicy": "IfNotPresent",
						"name": "deck",
						"ports": [
							{
								"containerPort": 8080,
								"name": "http",
								"protocol": "TCP"
							}
						],
						"resources": {},
						"terminationMessagePath": "/dev/termination-log",
						"terminationMessagePolicy": "File"
					}
				],
				"dnsPolicy": "ClusterFirst",
				"nodeName": "testNode",
				"priority": 0,
				"restartPolicy": "Always",
				"schedulerName": "default-scheduler",
				"securityContext": {},
				"terminationGracePeriodSeconds": 30
			},
			"status": {
				"conditions": [
					{
						"lastProbeTime": null,
						"lastTransitionTime": "2019-03-13T03:07:11Z",
						"status": "True",
						"type": "Initialized"
					},
					{
						"lastProbeTime": null,
						"lastTransitionTime": "2019-04-11T05:50:54Z",
						"status": "True",
						"type": "Ready"
					},
					{
						"lastProbeTime": null,
						"lastTransitionTime": "2019-04-11T05:50:54Z",
						"status": "True",
						"type": "ContainersReady"
					},
					{
						"lastProbeTime": null,
						"lastTransitionTime": "2019-03-13T03:07:11Z",
						"status": "True",
						"type": "PodScheduled"
					}
				],
				"containerStatuses": [
					{
						"containerID": "docker://ced62c592a1c3bc6bc43f32eda0cc8ede08bcbfb68ee930e10597c8438d12e16",
						"image": "gcr.io/k8s-prow/deck:v20190312-abfe0e0",
						"imageID": "docker-pullable://gcr.io/k8s-prow/deck@sha256:42ae1fe9f2048ec8ff2d87de463acdc3b6304fbf356d7a7badf379a572fb85d3",
						"lastState": {},
						"name": "deck",
						"ready": true,
						"restartCount": 0,
						"state": {
							"running": {
								"startedAt": "2019-03-13T03:07:43Z"
							}
						}
					}
				],
				"hostIP": "192.168.98.3",
				"phase": "Running",
				"podIP": "10.244.1.45",
				"qosClass": "BestEffort",
				"startTime": "2019-03-13T03:07:11Z"
			}
		}
		`
	})

	It("Should work well with k8s to get current Node", func() {
		node := &corev1.Node{}
		node.Name = nodeName
		pod1 := &corev1.Pod{}
		reader := strings.NewReader(podTemplate)
		err := yaml.NewYAMLOrJSONDecoder(reader, 10).Decode(pod1)
		Expect(err).ShouldNot(HaveOccurred(), "Failed to decode json")

		fakeClient := fake.NewSimpleClientset(node, pod1)
		k8sHelper := NewK8sHelper(fakeClient)
		Expect(k8sHelper.Start(stopCh)).ShouldNot(HaveOccurred())
		n, err := k8sHelper.GetCurrentNode()
		Expect(err).ShouldNot(HaveOccurred())
		Expect(n).To(Equal(node))
		Eventually(func() int {
			pods, err := k8sHelper.GetCurrentNodePods()
			if err != nil {
				log.Println(err)
				return -1
			}
			return len(pods)
		}, time.Second*20, time.Second*5).Should(Equal(1))
		testKey := "testKey"
		testVal := "testVal"
		Expect(k8sHelper.UpdateNodeAnnotation(testKey, testVal)).ShouldNot(HaveOccurred())
		node, err = k8sHelper.GetCurrentNode()
		Expect(err).ShouldNot(HaveOccurred())
		Expect(node.Annotations).To(HaveKey(testKey))
	})
})
