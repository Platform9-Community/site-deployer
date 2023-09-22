/*
Copyright 2023.

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

package controller

import (
	"context"
	"fmt"
	"time"

	"golang.org/x/exp/slices"
	batchv1 "k8s.io/api/batch/v1"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	deployv1 "github.com/Platform9-Community/site-deployer/api/v1"
	"github.com/go-logr/logr"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// SiteReconciler reconciles a Site object
type SiteReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

var (
	appName       = "site-deployer"
	siteFinalizer = "deploy.pf9.io/finalizer"
)

type ReconcilerEventType string

const (
	ReconcilerEventTypeCreating ReconcilerEventType = "add"
	ReconcilerEventTypeDeleted  ReconcilerEventType = "delete"
)

func (c ReconcilerEventType) String() string {
	return string(c)
}

//+kubebuilder:rbac:groups=deploy.pf9.io,resources=sites,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=deploy.pf9.io,resources=sites/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=deploy.pf9.io,resources=sites/finalizers,verbs=update

// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete

func RemoveString(s []string, r string) []string {
	for i, v := range s {
		if v == r {
			return append(s[:i], s[i+1:]...)
		}
	}
	return s
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.15.0/pkg/reconcile

func (r *SiteReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	ctxlog := log.FromContext(ctx)
	var eventType ReconcilerEventType

	site := &deployv1.Site{}
	if err := r.Get(ctx, req.NamespacedName, site); err != nil {
		if apierrors.IsNotFound(err) {
			ctxlog.Info("Received ignorable event for a recently deleted Site.")
			return ctrl.Result{}, nil
		}
		ctxlog.Error(err, fmt.Sprintf("Unexpected error reading Site '%s' object", site.Name))
		return ctrl.Result{}, err
	}

	// examine DeletionTimestamp to determine if object is under deletion or not
	if site.ObjectMeta.DeletionTimestamp.IsZero() {
		// The object is not being deleted
		eventType = ReconcilerEventTypeCreating
		// Check if finalizer exists and if not, add one
		if !slices.Contains(site.ObjectMeta.Finalizers, siteFinalizer) {
			ctxlog.Info(fmt.Sprintf("Site '%s' CR is being created or updated", site.Name))
			ctxlog.Info(fmt.Sprintf("Adding finalizer to Site '%s'", site.Name))
			site.ObjectMeta.Finalizers = append(site.ObjectMeta.Finalizers, siteFinalizer)
			if err := r.Update(context.Background(), site); err != nil {
				return reconcile.Result{}, err
			}
		}

		if res, err := r.ReconcileSiteJob(ctx, site, eventType, ctxlog); err != nil {
			return res, err
		}
	} else {
		// The object is being deleted
		eventType = ReconcilerEventTypeDeleted
		ctxlog.Info(fmt.Sprintf("Site '%s' CR is being deleted", site.Name))
		if !site.Spec.AllowQbertDeletion {
			ctxlog.Info(fmt.Sprintf("Site '%s' will not be deleted from Qbert API since allowQbertDeletion is false", site.Name))
		} else {
			if res, err := r.ReconcileSiteJob(ctx, site, eventType, ctxlog); err != nil {
				return res, err
			}
			var siteJobs batchv1.JobList
			if err := r.List(ctx, &siteJobs, client.InNamespace(site.Namespace), client.MatchingLabels{"deploy.pf9.io/site": site.Name, "deploy.pf9.io/action": "delete"}); err != nil {
				ctxlog.Error(err, fmt.Sprintf("Unable to list 'delete' type jobs for Site '%s'", site.Name))
				return ctrl.Result{}, err
			}
			var jobCompleted bool
			for _, job := range siteJobs.Items {
				if job.Status.Succeeded > 0 {
					ctxlog.Info(fmt.Sprintf("Identified 'delete' type job ('%s') that has completed successfully for Site '%s'", job.Name, site.Name))
					jobCompleted = true
				}
			}
			// Requeue if the job is not yet complete.
			if !jobCompleted {
				ctxlog.Info(fmt.Sprintf("Requeuing so that jobs for Site '%s' can have more time to complete", site.Name))
				return ctrl.Result{RequeueAfter: time.Second * 15}, nil
			}
		}
		// Now that the SiteJob has completed deletion tasks, we can remove the finalizer
		// so that the Site object can be deleted
		if slices.Contains(site.ObjectMeta.Finalizers, siteFinalizer) {
			ctxlog.Info(fmt.Sprintf("Removing finalizer from Site '%s' so that it can be deleted", site.Name))
			site.ObjectMeta.Finalizers = RemoveString(site.ObjectMeta.Finalizers, siteFinalizer)
			if err := r.Update(context.Background(), site); err != nil {
				return reconcile.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	return ctrl.Result{}, nil
}

func (r *SiteReconciler) ReconcileSiteJob(ctx context.Context, site *deployv1.Site, eventType ReconcilerEventType, ctxlog logr.Logger) (ctrl.Result, error) {
	if eventType == ReconcilerEventTypeDeleted {
		// We want to check if there any existing "add" type jobs already running
		// for this Site. If so, they need to be deleted first, before we create any "delete" jobs.
		var siteJobs batchv1.JobList
		if err := r.List(ctx, &siteJobs, client.InNamespace(site.Namespace), client.MatchingLabels{"deploy.pf9.io/site": site.Name, "deploy.pf9.io/action": "add"}); err != nil {
			ctxlog.Error(err, fmt.Sprintf("Unable to list 'add' type jobs for Site '%s'", site.Name))
			return ctrl.Result{}, err
		}
		for _, job := range siteJobs.Items {
			ctxlog.Info(fmt.Sprintf("Identified 'add' type job ('%s') that needs to be deleted first before creating a 'delete' job.", job.Name))
			if err := r.Delete(ctx, &job, client.PropagationPolicy(metav1.DeletePropagationBackground)); client.IgnoreNotFound(err) != nil {
				ctxlog.Error(err, fmt.Sprintf("Unable to delete job '%s'", job.Name))
			} else {
				ctxlog.Info(fmt.Sprintf("Deleted existing 'add' type job: '%s'", job.Name))
			}
		}
	}

	// Create a ConfigMap to store the qbertCluster json payload
	siteNamedResource := appName + "-" + site.Spec.ClusterName
	siteConfigmap := &corev1.ConfigMap{}
	err := r.Get(ctx, types.NamespacedName{Name: siteNamedResource, Namespace: site.Namespace}, siteConfigmap)
	if err != nil && apierrors.IsNotFound(err) {
		ctxlog.Info(fmt.Sprintf("Creating new ConfigMap '%s' for qbertCluster payload", siteNamedResource))
		siteConfigmap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      siteNamedResource,
				Namespace: site.Namespace,
			},
			Data: map[string]string{
				"qbertPayload": site.Spec.QbertCluster,
			},
		}
		err = ctrl.SetControllerReference(site, siteConfigmap, r.Scheme)
		if err != nil {
			return ctrl.Result{}, err
		}
		err = r.Create(ctx, siteConfigmap)
		if err != nil {
			ctxlog.Error(err, fmt.Sprintf("Failed to create ConfigMap for Site '%s'", site.Name))
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// Add the volume mounts from the Secret
	jobVolumeMounts := []corev1.VolumeMount{}
	for _, secret_item := range site.Spec.InfraJob.Mounts {
		mount := corev1.VolumeMount{
			Name:      site.Spec.InfraJob.SecretName + "-volume",
			MountPath: secret_item.MountPath,
			ReadOnly:  secret_item.ReadOnly,
			SubPath:   secret_item.SubPath,
		}
		jobVolumeMounts = append(jobVolumeMounts, mount)
	}
	qbertpayloadMount := corev1.VolumeMount{
		Name:      siteNamedResource + "-qbertpayload-volume",
		MountPath: "/" + appName,
		ReadOnly:  true,
	}
	jobVolumeMounts = append(jobVolumeMounts, qbertpayloadMount)

	// Setup the job's environment
	jobEnvVars := []corev1.EnvVar{
		{
			Name:  "SITE_DEPLOYER_JOB_CLUSTER_NAME",
			Value: site.Spec.ClusterName,
		},
		{
			Name:  "SITE_DEPLOYER_JOB_CMD",
			Value: eventType.String(),
		},
		{
			Name:  "SITE_DEPLOYER_JOB_ARGO_REPO",
			Value: site.Spec.ArgoSrc.RepoURL,
		},
		{
			Name:  "SITE_DEPLOYER_JOB_ARGO_TARGET_REV",
			Value: site.Spec.ArgoSrc.TargetRevision,
		},
		{
			Name:  "SITE_DEPLOYER_JOB_ARGO_PATH",
			Value: site.Spec.ArgoSrc.Path,
		},
	}
	for _, env_item := range site.Spec.InfraJob.Env {
		env := corev1.EnvVar{
			Name: env_item.Name,
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: site.Spec.InfraJob.SecretName,
					},
					Key: env_item.SubPath,
				},
			},
		}
		jobEnvVars = append(jobEnvVars, env)
	}

	siteJobName := siteNamedResource + "-" + eventType.String()
	siteJob := &batchv1.Job{}
	err = r.Get(ctx, types.NamespacedName{Name: siteJobName, Namespace: site.Namespace}, siteJob)
	if err != nil && apierrors.IsNotFound(err) {
		ctxlog.Info(fmt.Sprintf("Creating new Job '%s'", siteJobName))
		configmapMode := int32(0554)
		siteJob = &batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{
				Name:      siteJobName,
				Namespace: site.Namespace,
			},
			Spec: batchv1.JobSpec{
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{"deploy.pf9.io/site": site.Name, "deploy.pf9.io/action": eventType.String()},
					},
					Spec: corev1.PodSpec{
						RestartPolicy: corev1.RestartPolicyNever,
						Volumes: []corev1.Volume{
							{
								Name: siteNamedResource + "-qbertpayload-volume",
								VolumeSource: corev1.VolumeSource{
									ConfigMap: &corev1.ConfigMapVolumeSource{
										LocalObjectReference: corev1.LocalObjectReference{
											Name: siteNamedResource,
										},
										DefaultMode: &configmapMode,
									},
								},
							},
							{
								Name: site.Spec.InfraJob.SecretName + "-volume",
								VolumeSource: corev1.VolumeSource{
									Secret: &corev1.SecretVolumeSource{
										SecretName: site.Spec.InfraJob.SecretName,
									},
								},
							},
						},
						Containers: []corev1.Container{
							{
								Name:            appName,
								Image:           site.Spec.InfraJob.Image,
								ImagePullPolicy: "Always",
								Command:         site.Spec.InfraJob.Cmd,
								Args:            site.Spec.InfraJob.Args,
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										corev1.ResourceCPU:    *resource.NewMilliQuantity(int64(50), resource.DecimalSI),
										corev1.ResourceMemory: *resource.NewScaledQuantity(int64(250), resource.Mega),
									},
									/*
										Limits: corev1.ResourceList{
											corev1.ResourceCPU:    *resource.NewMilliQuantity(int64(100), resource.DecimalSI),
											corev1.ResourceMemory: *resource.NewScaledQuantity(int64(500), resource.Mega),
										},
									*/
								},
								Env:          jobEnvVars,
								VolumeMounts: jobVolumeMounts,
							},
						},
					},
				},
			},
		}
		err = ctrl.SetControllerReference(site, siteJob, r.Scheme)
		if err != nil {
			return ctrl.Result{}, err
		}
		err = r.Create(ctx, siteJob)
		if err != nil {
			ctxlog.Error(err, fmt.Sprintf("Failed to create Job '%s'", siteJobName))
			return ctrl.Result{}, err
		}
		ctxlog.Info(fmt.Sprintf("Job '%s' queued for Site '%s'", siteJobName, site.Name))
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *SiteReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&deployv1.Site{}).
		Complete(r)
}
