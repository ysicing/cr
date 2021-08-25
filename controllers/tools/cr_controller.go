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
	"context"
	"fmt"
	"github.com/ysicing/cr/pkg/util"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	corelisters "k8s.io/client-go/listers/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/source"
	"sync"

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

	if err := r.syncSaSecrets(ctx, cr); err != nil {
		return ctrl.Result{}, err
	}

	r.updateStatus(cr)

	return ctrl.Result{}, nil
}

func (r *CRReconciler) syncSaSecrets(ctx context.Context, cr *crv1beta1.CR) error {
	r.Recorder.Event(cr, corev1.EventTypeNormal, "Syncing", "Syncing secrets")
	nsList, err := r.NSLister.List(labels.Everything())
	if err != nil {
		return fmt.Errorf("list ns err: %v", err)
	}
	var nss []string
	if cr.Spec.WatchNamespace == "all" {
		for _, ns := range nsList {
			nss = append(nss, ns.Name)
		}
	} else {
		nss = append(nss, strings.Split(cr.Spec.WatchNamespace, ",")...)
	}
	return r.sync(ctx, cr, nss)
}

func (r *CRReconciler) sync(ctx context.Context, cr *crv1beta1.CR, nss []string) error {
	var wg sync.WaitGroup
	ch := make(chan bool, 5)
	for _, ns := range nss {
		wg.Add(1)
		go r.manageSA(ctx, ns, cr, ch, &wg)
	}
	wg.Wait()
	return nil
}

func (r *CRReconciler) manageSA(ctx context.Context, ns string, cr *crv1beta1.CR, ch chan bool, wg *sync.WaitGroup) {
	defer func() {
		wg.Done()
		<-ch
	}()

	ch <- true
	sa := &corev1.ServiceAccount{}
	saKey := client.ObjectKey{
		Name:      cr.Spec.ServiceAccount,
		Namespace: ns,
	}
	err := r.Get(ctx, saKey, sa)
	if err != nil {
		if !errors.IsNotFound(err) {
			klog.Errorf("failed to get sa %v: %v", saKey.Name, err)
			return
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
			klog.Errorf("failed to create sa %v, err: %v", saKey.Name, err)
			return
		}
		return
	}

	if len(ips) > 0 {
		sa.ImagePullSecrets = ips
		if err := r.Update(ctx, sa); err != nil {
			klog.Errorf("failed to update sa %v, err: %v", saKey.Name, err)
			return
		}
	}
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
				".dockerconfigjson": util.GenDockerConfigJSON(info[2], info[0], info[1]),
			},
			Type: corev1.SecretTypeDockerConfigJson,
		}
		if err := r.Create(ctx, secret); err != nil {
			if !errors.IsAlreadyExists(err) {
				klog.Errorf("failed to create sa %v, err: %v", secretKey.Name, err)
				r.Recorder.Event(cr, corev1.EventTypeWarning, "Error", fmt.Sprintf("add %v secrets, err: %v", secretKey.Name, err))

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

func (r *CRReconciler) updateStatus(cr *crv1beta1.CR) {
	r.Recorder.Event(cr, corev1.EventTypeNormal, "Synced", "Successfully sync secrets")
}

// SetupWithManager sets up the controller with the Manager.
func (r *CRReconciler) SetupWithManager(mgr ctrl.Manager) error {
	c, err := controller.New("cr", mgr, controller.Options{
		Reconciler:              r,
		MaxConcurrentReconciles: 3,
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
