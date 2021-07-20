/*
Copyright 2021 The 51talk EFF.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package tools

import (
	"bytes"
	"context"
	b64 "encoding/base64"
	"fmt"
	"github.com/ysicing/pkg/util/requeueduration"
	"html/template"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	corelisters "k8s.io/client-go/listers/core/v1"

	"strings"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	"math/rand"
	"time"

	crv1beta1 "github.com/ysicing/apis/tools/v1beta1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	minRequeueDuration = 3 * time.Second
)

// CRReconciler reconciles a CR object
type CRReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
	NSLister corelisters.NamespaceLister
}

//+kubebuilder:rbac:groups=core,resources=namespaces,verbs=get;list;watch
//+kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch
//+kubebuilder:rbac:groups=core,resources=secrets,verbs=create;update;patch;get;list;watch
//+kubebuilder:rbac:groups=core,resources=serviceaccounts,verbs=create;update;patch;get;list;watch
//+kubebuilder:rbac:groups=tools.51talk.me,resources=crs,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=tools.51talk.me,resources=crs/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=tools.51talk.me,resources=crs/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the CR object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.8.3/pkg/reconcile
func (r *CRReconciler) Reconcile(ctx context.Context, req ctrl.Request) (res ctrl.Result, err error) {
	startTime := time.Now()
	defer func() {
		if err == nil {
			if res.Requeue || res.RequeueAfter > 0 {
				klog.Infof("Finished syncing CR %s, cost %v, result: %v", req, time.Since(startTime), res)
			} else {
				klog.Infof("Finished syncing CR %s, cost %v", req, time.Since(startTime))
			}
		} else {
			klog.Errorf("Failed syncing CR %s: %v", req, err)
		}
	}()

	klog.Infof("ns: %v", req.Namespace)

	// Fetch CR
	cr := &crv1beta1.CR{}
	err = r.Get(ctx, req.NamespacedName, cr)
	if err != nil {
		if !errors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("failed to get cr %s: %v", req.NamespacedName.Name, err)
		}
		cr = nil
	}

	// if ns exist
	if cr == nil {
		return ctrl.Result{}, nil
	}

	duration := &requeueduration.Duration{}

	cr.Check()

	if modified, err := r.updateSaSecrets(ctx, cr); err != nil {
		return ctrl.Result{}, err
	} else if modified {
		return ctrl.Result{}, nil
	}
	if err = r.updateStatus(); err != nil {
		return ctrl.Result{}, err
	}
	res = ctrl.Result{}
	res.RequeueAfter, _ = duration.GetWithMsg()
	if res.RequeueAfter > 0 && res.RequeueAfter < minRequeueDuration {
		res.RequeueAfter = minRequeueDuration + time.Duration(rand.Int31n(2000))*time.Millisecond
	}
	return res, nil
}

func (r *CRReconciler) updateSaSecrets(ctx context.Context, cr *crv1beta1.CR) (bool, error) {
	nsList, err := r.NSLister.List(labels.Everything())
	if err != nil {
		return false, fmt.Errorf("list ns err: %v", err)
	}

	if cr.Spec.WatchNamespace == "all" {
		for _, ns := range nsList {
			klog.Infof("ns: %v, sa: %v", ns.Name, cr.Spec.ServiceAccount)
			r.manageSA(ctx, ns.Name, cr)
		}
	} else {
		for _, ns := range strings.Split(cr.Spec.WatchNamespace, ",") {
			klog.Infof("ns: %v, sa: %v", ns, cr.Spec.ServiceAccount)
			r.manageSA(ctx, ns, cr)
		}
	}
	return true, nil
}

func (r *CRReconciler) manageSA(ctx context.Context, ns string, cr *crv1beta1.CR) error {
	sa := &corev1.ServiceAccount{}
	saKey := client.ObjectKey{
		Name:      cr.Spec.ServiceAccount,
		Namespace: ns,
	}
	err := r.Get(ctx, saKey, sa)
	if err != nil {
		if !errors.IsNotFound(err) {
			return fmt.Errorf("failed to get sa %v: %v", saKey.Name, err)
		}
		sa = nil
	}
	ips := r.manageCrSecrets(ctx, ns, cr)
	if sa == nil {
		sa = &corev1.ServiceAccount{
			TypeMeta: metav1.TypeMeta{
				Kind:       "ServiceAccount",
				APIVersion: "v1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      saKey.Name,
				Namespace: saKey.Namespace,
			},
			ImagePullSecrets: ips,
		}
		if err := r.Create(ctx, sa); err != nil {
			return fmt.Errorf("failed to create sa %v, err: %v", saKey.Name, err)
		}
		return nil
	}

	if len(ips) > 0 {
		sa.ImagePullSecrets = ips
		if err := r.Update(ctx, sa); err != nil {
			return fmt.Errorf("failed to update sa %v, err: %v", saKey.Name, err)
		}
	}

	return nil
}

func (r *CRReconciler) manageCrSecrets(ctx context.Context, ns string, cr *crv1beta1.CR) (res []corev1.LocalObjectReference) {
	for _, domain := range strings.Split(cr.Spec.Domain, ",") {
		info := strings.Split(domain, ":")
		if len(info) != 3 {
			continue
		}
		secret := &corev1.Secret{}
		secretKey := client.ObjectKey{
			Name:      info[2],
			Namespace: ns,
		}
		err := r.Get(ctx, secretKey, secret)
		if err != nil {
			if !errors.IsNotFound(err) {
				klog.Errorf("failed to get sa %v: %v", secretKey.Name, err)
				continue
			}
			secret = nil
		}
		secret = &corev1.Secret{
			TypeMeta: metav1.TypeMeta{
				Kind:       "Secret",
				APIVersion: "v1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      secretKey.Name,
				Namespace: secretKey.Namespace,
			},
			StringData: map[string]string{
				".dockerconfigjson": parse(info[2], info[0], info[1]),
			},
			Type: corev1.SecretTypeDockerConfigJson,
		}
		if err := r.Create(ctx, secret); err != nil {
			klog.Errorf("failed to create sa %v, err: %v", secretKey.Name, err)
			continue
		}
		res = append(res, corev1.LocalObjectReference{
			Name: secretKey.Name,
		})
	}
	return
}

func (r *CRReconciler) updateStatus() error {
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *CRReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&crv1beta1.CR{}).
		Complete(r)
}

type ConfigJson struct {
	CRHost string
	CRUSER string
	CRPASS string
	CRAUTH string
}

func parse(domain, user, pass string) string {
	var b bytes.Buffer
	j := ConfigJson{
		CRHost: domain,
		CRUSER: user,
		CRPASS: B64EnCode(pass),
		CRAUTH: B64EnCode(fmt.Sprintf("%v:%v", user, pass)),
	}
	t, _ := template.New("configjson").Parse(dockerconfigjson)
	t.Execute(&b, j)
	return b.String()
}

// B64EnCode base64加密
func B64EnCode(code string) string {
	return b64.StdEncoding.EncodeToString([]byte(code))
}

const dockerconfigjson = `
{
  "auths": {
    "{{.CRHost}}": {
      "username": "{{.CRUSER}}",
      "password": "{{.CRPASS}}",
      "auth": "{{.CRAUTH}}"
    }
  }
}
`
