// Copyright 2026 the gpuaudit authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/gpuaudit/cli/internal/models"
)

// ScanOptions controls Kubernetes GPU scanning.
type ScanOptions struct {
	Kubeconfig   string
	Context      string
	PromURL      string
	PromEndpoint string
}

// Scan discovers GPU nodes in Kubernetes clusters accessible via kubeconfig.
func Scan(ctx context.Context, opts ScanOptions) ([]models.GPUInstance, error) {
	// Check if any kubeconfig is available
	if opts.Kubeconfig == "" && os.Getenv("KUBECONFIG") == "" {
		if _, err := os.Stat(defaultKubeconfig()); os.IsNotExist(err) {
			return nil, nil // no kubeconfig anywhere, skip silently
		}
	}

	fmt.Fprintf(os.Stderr, "  Scanning Kubernetes cluster for GPU nodes...\n")

	client, clusterName, err := buildClient(opts.Kubeconfig, opts.Context)
	if err != nil {
		return nil, fmt.Errorf("building k8s client: %w", err)
	}

	instances, err := DiscoverGPUNodes(ctx, client, clusterName)
	if err != nil {
		return nil, fmt.Errorf("discovering GPU nodes: %w", err)
	}

	return instances, nil
}

// BuildClientPublic builds a K8s client and returns the cluster name.
func BuildClientPublic(kubeconfigPath, contextName string) (K8sClient, string, error) {
	return buildClient(kubeconfigPath, contextName)
}

func buildClient(kubeconfigPath, contextName string) (K8sClient, string, error) {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeconfigPath != "" {
		loadingRules.ExplicitPath = kubeconfigPath
	}
	overrides := &clientcmd.ConfigOverrides{}
	if contextName != "" {
		overrides.CurrentContext = contextName
	}

	kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides)

	// Get the cluster name from the current context
	rawConfig, err := kubeConfig.RawConfig()
	if err != nil {
		return nil, "", fmt.Errorf("reading kubeconfig: %w", err)
	}

	currentContext := rawConfig.CurrentContext
	if contextName != "" {
		currentContext = contextName
	}

	clusterName := currentContext // use context name as cluster name
	if ctxObj, ok := rawConfig.Contexts[currentContext]; ok {
		clusterName = ctxObj.Cluster
	}

	restConfig, err := kubeConfig.ClientConfig()
	if err != nil {
		return nil, "", fmt.Errorf("building rest config: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, "", fmt.Errorf("creating clientset: %w", err)
	}

	return &k8sClientWrapper{clientset: clientset}, clusterName, nil
}

type k8sClientWrapper struct {
	clientset *kubernetes.Clientset
}

func (w *k8sClientWrapper) ListNodes(ctx context.Context, opts metav1.ListOptions) (*corev1.NodeList, error) {
	return w.clientset.CoreV1().Nodes().List(ctx, opts)
}

func (w *k8sClientWrapper) ListPods(ctx context.Context, namespace string, opts metav1.ListOptions) (*corev1.PodList, error) {
	return w.clientset.CoreV1().Pods(namespace).List(ctx, opts)
}

func (w *k8sClientWrapper) ProxyGet(ctx context.Context, namespace, podName, port, path string) ([]byte, error) {
	return w.clientset.CoreV1().Pods(namespace).ProxyGet("http", podName, port, path, nil).DoRaw(ctx)
}

func defaultKubeconfig() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".kube", "config")
}
