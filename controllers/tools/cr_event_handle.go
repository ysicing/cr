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

package tools

import (
	"context"
	"fmt"
	crv1beta1 "github.com/ysicing/cr/apis/tools/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

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
