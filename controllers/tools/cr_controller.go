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
	"html/template"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"strings"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	"time"

	crv1beta1 "github.com/ysicing/cr/apis/tools/v1beta1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
// +kubebuilder:rbac:groups=core,resources=events,verbs=get;list;watch;create;update;patch;delete
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
				klog.Infof("Finished syncing CR %v, cost %v, result: %v", req, time.Since(startTime), res)
			} else {
				klog.Infof("Finished syncing CR %v, cost %v", req, time.Since(startTime))
			}
		} else {
			klog.Errorf("Failed syncing CR %v: %v", req, err)
		}
	}()

	// Fetch CR
	cr := &crv1beta1.CR{}
	if err := r.Get(ctx, req.NamespacedName, cr); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	cr.Check()

	if modified, err := r.updateSaSecrets(ctx, cr); err != nil {
		return ctrl.Result{}, err
	} else if modified {
		return ctrl.Result{}, nil
	}
	if err = r.updateStatus(); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *CRReconciler) updateSaSecrets(ctx context.Context, cr *crv1beta1.CR) (bool, error) {
	nsList, err := r.NSLister.List(labels.Everything())
	if err != nil {
		return false, fmt.Errorf("list ns err: %v", err)
	}

	if cr.Spec.WatchNamespace == "all" {
		for _, ns := range nsList {
			klog.V(5).Infof("ns: %v, sa: %v", ns.Name, cr.Spec.ServiceAccount)
			if err := r.manageSA(ctx, ns.Name, cr); err != nil {
				klog.Errorf("manage %s sa %v, err: %v", ns.Name, cr.Spec.ServiceAccount, err)
			}
		}
	} else {
		for _, ns := range strings.Split(cr.Spec.WatchNamespace, ",") {
			klog.V(5).Infof("ns: %v, sa: %v", ns, cr.Spec.ServiceAccount)
			if err := r.manageSA(ctx, ns, cr); err != nil {
				klog.Errorf("manage %s sa %v, err: %v", ns, cr.Spec.ServiceAccount, err)
			}
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
			// secret = nil
		}
		secret = &corev1.Secret{
			TypeMeta: metav1.TypeMeta{
				Kind:       "Secret",
				APIVersion: "v1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("cr-%v", secretKey.Name),
				Namespace: secretKey.Namespace,
				Labels: map[string]string{
					"tools.51talk.me/component": "cr",
					"owner":                     cr.Name,
				},
			},
			StringData: map[string]string{
				".dockerconfigjson": parse(info[2], info[0], info[1]),
			},
			Type: corev1.SecretTypeDockerConfigJson,
		}
		if err := r.Create(ctx, secret); err != nil {
			if !errors.IsAlreadyExists(err) {
				klog.Errorf("failed to create sa %v, err: %v", secretKey.Name, err)
				r.Recorder.Event(cr, corev1.EventTypeWarning, "Error", fmt.Sprintf("add %v secrets, err: %v", secretKey.Name, err))

			} else {
				klog.Infof("docker secret %v exist", secretKey.Name)
				r.Recorder.Event(cr, corev1.EventTypeWarning, "Exist", err.Error())
			}
			continue
		}
		r.Recorder.Event(cr, corev1.EventTypeNormal, "Success", fmt.Sprintf("add %v secrets", secretKey.Name))
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
	c, err := controller.New("cr", mgr, controller.Options{
		Reconciler: r,
	})
	if err != nil {
		return err
	}
	err = c.Watch(&source.Kind{Type: &corev1.Namespace{}}, &nsEventHandler{reader: mgr.GetCache()})
	if err != nil {
		return err
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&crv1beta1.CR{}).
		Complete(r)
}

type nsEventHandler struct {
	reader client.Reader
}

func (e *nsEventHandler) Create(evt event.CreateEvent, q workqueue.RateLimitingInterface) {
	crList := &crv1beta1.CRList{}
	err := e.reader.List(context.TODO(), crList)
	if err != nil {
		klog.V(6).Infof("Error enqueueing ns list: %v", err)
		return
	}

	ns := evt.Object.(*corev1.Namespace)
	klog.V(6).Infof("add new ns: %v", ns.Name)
	for index, cr := range crList.Items {
		should, err := NsShouldRunCR(ns, &crList.Items[index])
		if err != nil {
			continue
		}
		if should {
			klog.V(6).Infof("new ns: %s triggers CR %s/%s to reconcile.", ns.Name, cr.GetNamespace(), cr.GetName())
			q.Add(reconcile.Request{NamespacedName: types.NamespacedName{
				Name:      cr.GetName(),
				Namespace: cr.GetNamespace(),
			}})
		}
	}
}

func (e *nsEventHandler) Update(evt event.UpdateEvent, q workqueue.RateLimitingInterface) {
}

func (e *nsEventHandler) Delete(evt event.DeleteEvent, q workqueue.RateLimitingInterface) {
}

func (e *nsEventHandler) Generic(evt event.GenericEvent, q workqueue.RateLimitingInterface) {
}

func NsShouldRunCR(ns *corev1.Namespace, cr *crv1beta1.CR) (bool, error) {
	// 检查是否需要配置cr
	if ns.Annotations == nil {
		return true, nil
	}
	key := fmt.Sprintf("cr-%v", cr.Name)
	if value, ok := ns.Annotations[key]; ok && value == "init" {
		return false, nil
	}
	return true, nil
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
	if err := t.Execute(&b, j); err != nil {
		return ""
	}
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
