/*
 * // Copyright (c) 2021. The 51talk EFF Team Authors.
 * //
 * // Licensed under the Apache License, Version 2.0 (the "License");
 * // you may not use this file except in compliance with the License.
 * // You may obtain a copy of the License at
 * //
 * //     http://www.apache.org/licenses/LICENSE-2.0
 * //
 * // Unless required by applicable law or agreed to in writing, software
 * // distributed under the License is distributed on an "AS IS" BASIS,
 * // See the License for the specific language governing permissions and
 * // limitations under the License.
 */

package main

import (
	"context"
	clientset "github.com/ysicing/cr/pkg/client/clientset/versioned"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
	"k8s.io/klog/v2"
	"os"
	"path/filepath"
)

func init() {
	klog.InitFlags(nil)
}

func main() {
	// Create client
	var kubeconfig string
	kubeconfig, ok := os.LookupEnv("KUBECONFIG")
	if !ok {
		kubeconfig = filepath.Join(homedir.HomeDir(), ".kube", "config")
	}

	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		panic(err)
	}
	client, err := clientset.NewForConfig(config)
	if err != nil {
		panic(err)
	}
	crs, err := client.ToolsV1beta1().CRs(corev1.NamespaceAll).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		if !errors.IsNotFound(err) {
			panic(err)
		}
	}
	for _, cr := range crs.Items {
		klog.Infof("ns %v, cr %v", cr.Namespace, cr.Name)
	}
}
