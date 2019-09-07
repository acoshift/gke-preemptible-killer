package main

import (
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
	"time"

	"github.com/ericchiang/k8s"
	corev1 "github.com/ericchiang/k8s/apis/core/v1"
	metav1 "github.com/ericchiang/k8s/apis/meta/v1"
	"github.com/ghodss/yaml"
	"github.com/rs/zerolog/log"
)

type Kubernetes struct {
	Client *k8s.Client
}

type KubernetesClient interface {
	DrainNode(context.Context, string, int, time.Duration) error
	DrainKubeDNSFromNode(context.Context, string, int) error
	GetNode(context.Context, string) (*corev1.Node, error)
	DeleteNode(context.Context, string) error
	GetPreemptibleNodes(context.Context) (*corev1.NodeList, error)
	GetProjectIdAndZoneFromNode(context.Context, string) (string, string, error)
	SetNodeAnnotation(context.Context, string, string, string) error
	SetUnschedulableState(context.Context, string, bool) error
}

// NewKubernetesClient return a Kubernetes client
func NewKubernetesClient(host string, port string, namespace string, kubeConfigPath string) (kubernetes KubernetesClient, err error) {
	var k8sClient *k8s.Client

	if len(host) > 0 && len(port) > 0 {
		k8sClient, err = k8s.NewInClusterClient()

		if err != nil {
			err = fmt.Errorf("Error loading incluster client:\n%v", err)
			return
		}
	} else if len(kubeConfigPath) > 0 {
		k8sClient, err = loadK8sClient(kubeConfigPath)

		if err != nil {
			err = fmt.Errorf("Error loading client using kubeconfig:\n%v", err)
			return
		}
	} else {
		if namespace == "" {
			namespace = "default"
		}

		k8sClient = &k8s.Client{
			Endpoint:  "http://127.0.0.1:8001",
			Namespace: namespace,
			Client:    &http.Client{},
		}
	}

	kubernetes = &Kubernetes{
		Client: k8sClient,
	}

	return
}

// GetProjectIdAndZoneFromNode returns project id and zone from given node name
// by getting informations from node spec provider id
func (k *Kubernetes) GetProjectIdAndZoneFromNode(ctx context.Context, name string) (projectId string, zone string, err error) {
	node, err := k.GetNode(ctx, name)

	if err != nil {
		return
	}

	s := strings.Split(*node.Spec.ProviderID, "/")
	projectId = s[2]
	zone = s[3]

	return
}

// GetPreemptibleNodes return a list of preemptible node
func (k *Kubernetes) GetPreemptibleNodes(ctx context.Context) (*corev1.NodeList, error) {
	labels := new(k8s.LabelSelector)
	labels.Eq("cloud.google.com/gke-preemptible", "true")

	var nodes corev1.NodeList
	err := k.Client.List(ctx, "", &nodes, labels.Selector())
	if err != nil {
		return nil, err
	}
	return &nodes, nil
}

// GetNode return the node object from given name
func (k *Kubernetes) GetNode(ctx context.Context, name string) (*corev1.Node, error) {
	var node corev1.Node
	err := k.Client.Get(ctx, "", name, &node)
	if err != nil {
		return nil, err
	}
	return &node, nil
}

func (k *Kubernetes) DeleteNode(ctx context.Context, name string) error {
	return k.Client.Delete(ctx, &corev1.Node{
		Metadata: &metav1.ObjectMeta{
			Name: k8s.String(name),
		},
	})
}

// SetNodeAnnotation add an annotation (key/value) to a node from a given node name
// As the nodes are constantly being updated, the k8s client doesn't support patch feature yet and
// to reduce the chance to hit a failure 409 we fetch the node before update
func (k *Kubernetes) SetNodeAnnotation(ctx context.Context, name string, key string, value string) error {
	node, err := k.GetNode(ctx, name)
	if err != nil {
		return fmt.Errorf("Error getting node information before setting annotation:\n%w", err)
	}

	node.Metadata.Annotations[key] = value
	return k.Client.Update(ctx, node)
}

// SetUnschedulableState set the unschedulable state of a given node name
func (k *Kubernetes) SetUnschedulableState(ctx context.Context, name string, unschedulable bool) (err error) {
	node, err := k.GetNode(ctx, name)
	if err != nil {
		return fmt.Errorf("Error getting node information before setting unschedulable state:\n%w", err)
	}

	node.Spec.Unschedulable = &unschedulable
	return k.Client.Update(ctx, node)
}

// filterOutPodByOwnerReferenceKind filter out a list of pods by its owner references kind
func filterOutPodByOwnerReferenceKind(podList []*corev1.Pod, kind string) (output []*corev1.Pod) {
	for _, pod := range podList {
		for _, ownerReference := range pod.Metadata.OwnerReferences {
			if *ownerReference.Kind != kind {
				output = append(output, pod)
			}
		}
	}

	return
}

// filterOutPodByNode filters out a list of pods by its node
func filterOutPodByNode(podList []*corev1.Pod, nodeName string) (output []*corev1.Pod) {
	for _, pod := range podList {
		if *pod.Spec.NodeName == nodeName {
			output = append(output, pod)
		}
	}

	return
}

// DrainNode delete every pods from a given node and wait that all pods are removed before it succeed
// it also make sure we don't select DaemonSet because they are not subject to unschedulable state
func (k *Kubernetes) DrainNode(ctx context.Context, name string, drainTimeout int, waitEachDeletion time.Duration) error {
	// Select all pods sitting on the node except the one from kube-system
	fieldSelector := k8s.QueryParam("fieldSelector", "spec.nodeName="+name+",metadata.namespace!=kube-system")
	var podList corev1.PodList
	err := k.Client.List(ctx, k8s.AllNamespaces, &podList, fieldSelector)
	if err != nil {
		return err
	}

	// Filter out DaemonSet from the list of pods
	filteredPodList := filterOutPodByOwnerReferenceKind(podList.Items, "DaemonSet")

	log.Info().
		Str("host", name).
		Msgf("%d pod(s) found", len(filteredPodList))

	for _, pod := range filteredPodList {
		log.Info().
			Str("host", name).
			Msgf("Deleting pod %s and wait %s...", *pod.Metadata.Name, waitEachDeletion)

		err = k.Client.Delete(ctx, pod)
		if err != nil {
			log.Error().
				Err(err).
				Str("host", name).
				Msgf("Error draining pod %s", *pod.Metadata.Name)
			continue
		}

		time.Sleep(waitEachDeletion)
	}

	doneDraining := make(chan bool)

	// Wait until all pods are deleted
	go func() {
		for {
			sleepTime := ApplyJitter(10)
			sleepDuration := time.Duration(sleepTime) * time.Second

			var pendingPodList corev1.PodList
			err := k.Client.List(ctx, k8s.AllNamespaces, &pendingPodList, fieldSelector)
			if err != nil {
				log.Error().
					Err(err).
					Str("host", name).
					Msgf("Error getting list of pods, sleeping %ds", sleepTime)

				time.Sleep(sleepDuration)
				continue
			}

			// Filter out DaemonSet from the list of pods
			filteredPendingPodList := filterOutPodByOwnerReferenceKind(pendingPodList.Items, "DaemonSet")
			podsPending := len(filteredPendingPodList)

			if podsPending == 0 {
				doneDraining <- true
				return
			}

			log.Info().
				Str("host", name).
				Msgf("%d pod(s) pending deletion, sleeping %ds", podsPending, sleepTime)

			time.Sleep(sleepDuration)
		}
	}()

	select {
	case <-doneDraining:
		break
	case <-time.After(time.Duration(drainTimeout) * time.Second):
		log.Warn().
			Str("host", name).
			Msg("Draining node timeout reached")
		return nil
	}

	log.Info().
		Str("host", name).
		Msg("Done draining node")

	return nil
}

// DrainKubeDNSFromNode deletes any kube-dns pods running on the node
func (k *Kubernetes) DrainKubeDNSFromNode(ctx context.Context, name string, drainTimeout int) error {
	// Select all pods sitting on the node except the one from kube-system
	labelSelector := k8s.QueryParam("labelSelector", "k8s-app=kube-dns")
	var podList corev1.PodList
	err := k.Client.List(ctx, "kube-system", &podList, labelSelector)
	if err != nil {
		return err
	}

	// Filter out pods running on other nodes
	filteredPodList := filterOutPodByNode(podList.Items, name)

	log.Info().
		Str("host", name).
		Msgf("%d kube-dns pod(s) found", len(filteredPodList))

	for _, pod := range filteredPodList {
		log.Info().
			Str("host", name).
			Msgf("Deleting pod %s", *pod.Metadata.Name)

		err = k.Client.Delete(ctx, pod)
		if err != nil {
			log.Error().
				Err(err).
				Str("host", name).
				Msgf("Error draining pod %s", *pod.Metadata.Name)
			continue
		}
	}

	doneDraining := make(chan bool)

	// Wait until all pods are deleted
	go func() {
		for {
			sleepTime := ApplyJitter(10)
			sleepDuration := time.Duration(sleepTime) * time.Second

			var podList corev1.PodList
			err := k.Client.List(ctx, "kube-system", &podList, labelSelector)
			if err != nil {
				log.Error().
					Err(err).
					Str("host", name).
					Msgf("Error getting list of kube-dns pods, sleeping %ds", sleepTime)

				time.Sleep(sleepDuration)
				continue
			}

			// Filter out DaemonSet from the list of pods
			filteredPendingPodList := filterOutPodByNode(podList.Items, name)
			podsPending := len(filteredPendingPodList)

			if podsPending == 0 {
				doneDraining <- true
				return
			}

			log.Info().
				Str("host", name).
				Msgf("%d pod(s) pending deletion, sleeping %ds", podsPending, sleepTime)

			time.Sleep(sleepDuration)
		}
	}()

	select {
	case <-doneDraining:
		break
	case <-time.After(time.Duration(drainTimeout) * time.Second):
		log.Warn().
			Str("host", name).
			Msg("Draining kube-dns node timeout reached")
		return nil
	}

	log.Info().
		Str("host", name).
		Msg("Done draining kube-dns from node")

	return nil
}

// loadK8sClient parses a kubeconfig from a file and returns a Kubernetes
// client. It does not support extensions or client auth providers.
func loadK8sClient(kubeconfigPath string) (*k8s.Client, error) {
	data, err := ioutil.ReadFile(kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("Read kubeconfig error:\n%v", err)
	}

	// Unmarshal YAML into a Kubernetes config object.
	var config k8s.Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("Unmarshal kubeconfig error:\n%v", err)
	}

	// fmt.Printf("%#v", config)
	return k8s.NewClient(&config)
}
