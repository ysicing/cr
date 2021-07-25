## 免密拉取镜像

### Usage

```
apiVersion: tools.51talk.me/v1beta1
kind: CR
metadata:
  name: cr-sample
spec:
  domain: root:pass:hub.baidu.com
  # sa可选, 默认default
  # sa: default
  # watchns可选,默认all
  # watchns: all
```

### Install

```bash
kubectl apply -f https://raw.githubusercontent.com/ysicing/cr/master/hack/deploy/crd.yaml
kubectl apply -f https://raw.githubusercontent.com/ysicing/cr/master/hack/deploy/cr.yaml
```

### Dev

参考 [example/client/main.go](example/client/main.go)

```bash
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
```