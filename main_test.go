package main

import (
	"context"
	"testing"
	"time"

	"github.com/ericchiang/k8s"
	corev1 "github.com/ericchiang/k8s/apis/core/v1"
	metav1 "github.com/ericchiang/k8s/apis/meta/v1"
	"github.com/rs/zerolog"
)

type FakeKubernetes struct {
}

func FakeNewKubernetesClient() KubernetesClient {
	return &FakeKubernetes{}
}

func (k *FakeKubernetes) GetProjectIdAndZoneFromNode(ctx context.Context, name string) (string, string, error) {
	return "", "", nil
}

func (k *FakeKubernetes) DrainNode(ctx context.Context, node string, drainTimeout int, waitEachDeletion time.Duration) error {
	return nil
}

func (k *FakeKubernetes) DrainKubeDNSFromNode(ctx context.Context, node string, drainTimeout int) error {
	return nil
}

func (k *FakeKubernetes) GetNode(ctx context.Context, name string) (*corev1.Node, error) {
	return &corev1.Node{}, nil
}

func (k *FakeKubernetes) DeleteNode(ctx context.Context, name string) error {
	return nil
}

func (k *FakeKubernetes) SetNodeAnnotation(ctx context.Context, name string, key string, value string) error {
	return nil
}
func (k *FakeKubernetes) SetUnschedulableState(ctx context.Context, name string, unschedulable bool) error {
	return nil
}

func (k *FakeKubernetes) GetPreemptibleNodes(ctx context.Context) (*corev1.NodeList, error) {
	return &corev1.NodeList{}, nil
}

func TestGetCurrentNodeState(t *testing.T) {
	zerolog.SetGlobalLevel(zerolog.Disabled)

	node := &corev1.Node{
		Metadata: &metav1.ObjectMeta{
			Name: k8s.String("node-1"),
			Annotations: map[string]string{
				"acoshift/gke-preemptible-killer-state": "2017-11-11T11:11:11Z",
			},
		},
	}

	state := getCurrentNodeState(node)

	if state.ExpiryDatetime != "2017-11-11T11:11:11Z" {
		t.Errorf("Expect expiry date time to be 2017-11-11T11:11:11Z, instead got %s", state.ExpiryDatetime)
	}
}

func TestGetDesiredNodeState(t *testing.T) {
	zerolog.SetGlobalLevel(zerolog.Disabled)

	creationTimestamp := time.Date(2017, 11, 11, 12, 00, 00, 0, time.UTC)
	creationTimestampUnix := creationTimestamp.Unix()
	creationTimestamp24HoursLater := creationTimestamp.Add(24 * time.Hour)

	node := &corev1.Node{
		Metadata: &metav1.ObjectMeta{
			Name:              k8s.String("node-1"),
			CreationTimestamp: &metav1.Time{Seconds: &creationTimestampUnix},
		},
	}

	client := FakeNewKubernetesClient()

	state, _ := getDesiredNodeState(client, node)
	stateTS, _ := time.Parse(time.RFC3339, state.ExpiryDatetime)

	if !creationTimestamp24HoursLater.After(stateTS) {
		t.Errorf("Expect expiry date time to before 24h after the creation date %s, instead got %s", creationTimestamp, state.ExpiryDatetime)
	}
}
