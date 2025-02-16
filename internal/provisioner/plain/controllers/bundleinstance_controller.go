/*
Copyright 2021.

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

package controllers

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"

	helmclient "github.com/operator-framework/helm-operator-plugins/pkg/client"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/release"
	"helm.sh/helm/v3/pkg/storage/driver"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/source"
	"sigs.k8s.io/yaml"

	rukpakv1alpha1 "github.com/operator-framework/rukpak/api/v1alpha1"
	helmpredicate "github.com/operator-framework/rukpak/internal/helm-operator-plugins/predicate"
	"github.com/operator-framework/rukpak/internal/storage"
	"github.com/operator-framework/rukpak/internal/util"
)

const (
	plainBundleProvisionerID = "core.rukpak.io/plain"
)

// BundleInstanceReconciler reconciles a BundleInstance object
type BundleInstanceReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	Controller controller.Controller

	ActionClientGetter helmclient.ActionClientGetter
	BundleStorage      storage.Storage
	ReleaseNamespace   string

	dynamicWatchMutex sync.RWMutex
	dynamicWatchGVKs  map[schema.GroupVersionKind]struct{}
}

//+kubebuilder:rbac:groups=core.rukpak.io,resources=bundleinstances,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core.rukpak.io,resources=bundleinstances/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=core.rukpak.io,resources=bundleinstances/finalizers,verbs=update
//+kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch
//+kubebuilder:rbac:groups=operators.coreos.com,resources=operatorgroups,verbs=get;list;watch
//+kubebuilder:rbac:groups=*,resources=*,verbs=*

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the BundleInstance object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.9.2/pkg/reconcile
func (r *BundleInstanceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	l := log.FromContext(ctx)
	l.V(1).Info("starting reconciliation")
	defer l.V(1).Info("ending reconciliation")

	bi := &rukpakv1alpha1.BundleInstance{}
	if err := r.Get(ctx, req.NamespacedName, bi); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	defer func() {
		bi := bi.DeepCopy()
		bi.ObjectMeta.ManagedFields = nil
		if err := r.Status().Patch(ctx, bi, client.Apply, client.FieldOwner(plainBundleProvisionerID)); err != nil {
			l.Error(err, "failed to patch status")
		}
	}()

	b := &rukpakv1alpha1.Bundle{}
	if err := r.Get(ctx, types.NamespacedName{Name: bi.Spec.BundleName}, b); err != nil {
		bundleStatus := metav1.ConditionUnknown
		if apierrors.IsNotFound(err) {
			bundleStatus = metav1.ConditionFalse
		}
		meta.SetStatusCondition(&bi.Status.Conditions, metav1.Condition{
			Type:    rukpakv1alpha1.TypeHasValidBundle,
			Status:  bundleStatus,
			Reason:  rukpakv1alpha1.ReasonBundleLookupFailed,
			Message: err.Error(),
		})
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	desiredObjects, err := r.loadBundle(ctx, bi)
	if err != nil {
		var bnuErr *errBundleNotUnpacked
		if errors.As(err, &bnuErr) {
			reason := fmt.Sprintf("BundleUnpack%s", b.Status.Phase)
			if b.Status.Phase == rukpakv1alpha1.PhaseUnpacking {
				reason = "BundleUnpackRunning"
			}
			meta.SetStatusCondition(&bi.Status.Conditions, metav1.Condition{
				Type:   rukpakv1alpha1.TypeInstalled,
				Status: metav1.ConditionFalse,
				Reason: reason,
			})
			return ctrl.Result{}, nil
		}
		meta.SetStatusCondition(&bi.Status.Conditions, metav1.Condition{
			Type:    rukpakv1alpha1.TypeHasValidBundle,
			Status:  metav1.ConditionFalse,
			Reason:  rukpakv1alpha1.ReasonBundleLoadFailed,
			Message: err.Error(),
		})
		return ctrl.Result{}, err
	}

	chrt := &chart.Chart{
		Metadata: &chart.Metadata{},
	}
	for _, obj := range desiredObjects {
		jsonData, err := yaml.Marshal(obj)
		if err != nil {
			meta.SetStatusCondition(&bi.Status.Conditions, metav1.Condition{
				Type:    rukpakv1alpha1.TypeInvalidBundleContent,
				Status:  metav1.ConditionTrue,
				Reason:  rukpakv1alpha1.ReasonReadingContentFailed,
				Message: err.Error(),
			})
			return ctrl.Result{}, err
		}
		hash := sha256.Sum256(jsonData)
		chrt.Templates = append(chrt.Templates, &chart.File{
			Name: fmt.Sprintf("object-%x.yaml", hash[0:8]),
			Data: jsonData,
		})
	}

	bi.SetNamespace(r.ReleaseNamespace)
	cl, err := r.ActionClientGetter.ActionClientFor(bi)
	bi.SetNamespace("")
	if err != nil {
		meta.SetStatusCondition(&bi.Status.Conditions, metav1.Condition{
			Type:    rukpakv1alpha1.TypeInstalled,
			Status:  metav1.ConditionFalse,
			Reason:  rukpakv1alpha1.ReasonErrorGettingClient,
			Message: err.Error(),
		})
		return ctrl.Result{}, err
	}

	rel, state, err := r.getReleaseState(cl, bi, chrt)
	if err != nil {
		meta.SetStatusCondition(&bi.Status.Conditions, metav1.Condition{
			Type:    rukpakv1alpha1.TypeInstalled,
			Status:  metav1.ConditionFalse,
			Reason:  rukpakv1alpha1.ReasonErrorGettingReleaseState,
			Message: err.Error(),
		})
		return ctrl.Result{}, err
	}

	switch state {
	case stateNeedsInstall:
		_, err = cl.Install(bi.Name, r.ReleaseNamespace, chrt, nil, func(install *action.Install) error {
			install.CreateNamespace = false
			return nil
		})
		if err != nil {
			meta.SetStatusCondition(&bi.Status.Conditions, metav1.Condition{
				Type:    rukpakv1alpha1.TypeInstalled,
				Status:  metav1.ConditionFalse,
				Reason:  rukpakv1alpha1.ReasonInstallFailed,
				Message: err.Error(),
			})
			return ctrl.Result{}, err
		}
	case stateNeedsUpgrade:
		_, err = cl.Upgrade(bi.Name, r.ReleaseNamespace, chrt, nil)
		if err != nil {
			meta.SetStatusCondition(&bi.Status.Conditions, metav1.Condition{
				Type:    rukpakv1alpha1.TypeInstalled,
				Status:  metav1.ConditionFalse,
				Reason:  rukpakv1alpha1.ReasonUpgradeFailed,
				Message: err.Error(),
			})
			return ctrl.Result{}, err
		}
	case stateUnchanged:
		if err := cl.Reconcile(rel); err != nil {
			meta.SetStatusCondition(&bi.Status.Conditions, metav1.Condition{
				Type:    rukpakv1alpha1.TypeInstalled,
				Status:  metav1.ConditionFalse,
				Reason:  rukpakv1alpha1.ReasonReconcileFailed,
				Message: err.Error(),
			})
			return ctrl.Result{}, err
		}
	default:
		return ctrl.Result{}, fmt.Errorf("unexpected release state %q", state)
	}

	for _, obj := range desiredObjects {
		uMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
		if err != nil {
			meta.SetStatusCondition(&bi.Status.Conditions, metav1.Condition{
				Type:    rukpakv1alpha1.TypeInstalled,
				Status:  metav1.ConditionFalse,
				Reason:  rukpakv1alpha1.ReasonCreateDynamicWatchFailed,
				Message: err.Error(),
			})
			return ctrl.Result{}, err
		}

		u := &unstructured.Unstructured{Object: uMap}
		if err := func() error {
			r.dynamicWatchMutex.Lock()
			defer r.dynamicWatchMutex.Unlock()

			_, isWatched := r.dynamicWatchGVKs[u.GroupVersionKind()]
			if !isWatched {
				if err := r.Controller.Watch(
					&source.Kind{Type: u},
					&handler.EnqueueRequestForOwner{OwnerType: bi, IsController: true},
					helmpredicate.DependentPredicateFuncs()); err != nil {
					return err
				}
				r.dynamicWatchGVKs[u.GroupVersionKind()] = struct{}{}
			}
			return nil
		}(); err != nil {
			meta.SetStatusCondition(&bi.Status.Conditions, metav1.Condition{
				Type:    rukpakv1alpha1.TypeInstalled,
				Status:  metav1.ConditionFalse,
				Reason:  rukpakv1alpha1.ReasonCreateDynamicWatchFailed,
				Message: err.Error(),
			})
			return ctrl.Result{}, err
		}
	}
	meta.SetStatusCondition(&bi.Status.Conditions, metav1.Condition{
		Type:   rukpakv1alpha1.TypeInstalled,
		Status: metav1.ConditionTrue,
		Reason: rukpakv1alpha1.ReasonInstallationSucceeded,
	})
	bi.Status.InstalledBundleName = bi.Spec.BundleName
	return ctrl.Result{}, nil
}

type releaseState string

const (
	stateNeedsInstall releaseState = "NeedsInstall"
	stateNeedsUpgrade releaseState = "NeedsUpgrade"
	stateUnchanged    releaseState = "Unchanged"
	stateError        releaseState = "Error"
)

func (r *BundleInstanceReconciler) getReleaseState(cl helmclient.ActionInterface, obj metav1.Object, chrt *chart.Chart) (*release.Release, releaseState, error) {
	currentRelease, err := cl.Get(obj.GetName())
	if err != nil && !errors.Is(err, driver.ErrReleaseNotFound) {
		return nil, stateError, err
	}
	if errors.Is(err, driver.ErrReleaseNotFound) {
		return nil, stateNeedsInstall, nil
	}
	desiredRelease, err := cl.Upgrade(obj.GetName(), r.ReleaseNamespace, chrt, nil, func(upgrade *action.Upgrade) error {
		upgrade.DryRun = true
		return nil
	})
	if err != nil {
		return currentRelease, stateError, err
	}
	if desiredRelease.Manifest != currentRelease.Manifest ||
		currentRelease.Info.Status == release.StatusFailed ||
		currentRelease.Info.Status == release.StatusSuperseded {
		return currentRelease, stateNeedsUpgrade, nil
	}
	return currentRelease, stateUnchanged, nil
}

type errBundleNotUnpacked struct {
	currentPhase string
}

func (err errBundleNotUnpacked) Error() string {
	const baseError = "bundle is not yet unpacked"
	if err.currentPhase == "" {
		return baseError
	}
	return fmt.Sprintf("%s, current phase=%s", baseError, err.currentPhase)
}

func (r *BundleInstanceReconciler) loadBundle(ctx context.Context, bi *rukpakv1alpha1.BundleInstance) ([]client.Object, error) {
	b := &rukpakv1alpha1.Bundle{}
	if err := r.Get(ctx, types.NamespacedName{Name: bi.Spec.BundleName}, b); err != nil {
		return nil, fmt.Errorf("get bundle %q: %w", bi.Spec.BundleName, err)
	}
	if b.Status.Phase != rukpakv1alpha1.PhaseUnpacked {
		return nil, &errBundleNotUnpacked{currentPhase: b.Status.Phase}
	}

	objects, err := r.BundleStorage.Load(ctx, b)
	if err != nil {
		return nil, fmt.Errorf("load bundle objects: %w", err)
	}

	objs := make([]client.Object, 0, len(objects))
	for _, obj := range objects {
		obj := obj
		obj.SetLabels(util.MergeMaps(obj.GetLabels(), map[string]string{
			"core.rukpak.io/owner-kind": "BundleInstance",
			"core.rukpak.io/owner-name": bi.Name,
		}))
		objs = append(objs, &obj)
	}
	return objs, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *BundleInstanceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	controller, err := ctrl.NewControllerManagedBy(mgr).
		For(&rukpakv1alpha1.BundleInstance{}, builder.WithPredicates(util.BundleInstanceProvisionerFilter(plainBundleProvisionerID))).
		Watches(&source.Kind{Type: &rukpakv1alpha1.Bundle{}}, handler.EnqueueRequestsFromMapFunc(util.MapBundleToBundleInstanceHandler(mgr.GetClient(), mgr.GetLogger()))).
		Build(r)
	if err != nil {
		return err
	}
	r.Controller = controller
	r.dynamicWatchGVKs = map[schema.GroupVersionKind]struct{}{}
	return nil
}
