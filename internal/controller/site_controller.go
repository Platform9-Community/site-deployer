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
	_ "embed"
	"time"

	zlog "github.com/rs/zerolog/log"

	batchv1 "k8s.io/api/batch/v1"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	deployv1 "github.com/jeremymv2/site-deployer/api/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// SiteReconciler reconciles a Site object
type SiteReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// /
// STEP 1: retrieve the script content from the codebase.
//
//go:embed embeds/task.sh

var taskScript string

//+kubebuilder:rbac:groups=deploy.ethzero.cloud,resources=sites,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=deploy.ethzero.cloud,resources=sites/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=deploy.ethzero.cloud,resources=sites/finalizers,verbs=update

// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Site object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.15.0/pkg/reconcile

func (r *SiteReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	ctxlog := log.FromContext(ctx)

	site := &deployv1.Site{}
	if err := r.Get(ctx, req.NamespacedName, site); err != nil {
		zlog.Error().Err(err).Msg("unable to fetch site")
		// we'll ignore not-found errors, since they can't be fixed by an immediate
		// requeue (we'll need to wait for a new notification), and we can get them
		// on deleted requests.
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// STEP 2: create the ConfigMap with the script's content.
	configmap := &corev1.ConfigMap{}
	err := r.Get(ctx, types.NamespacedName{Name: "sitedeployer", Namespace: site.Namespace}, configmap)
	if err != nil && apierrors.IsNotFound(err) {

		ctxlog.Info("Creating new ConfigMap")
		configmap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "sitedeployer",
				Namespace: site.Namespace,
			},
			Data: map[string]string{
				"task.sh": taskScript,
			},
		}

		err = ctrl.SetControllerReference(site, configmap, r.Scheme)
		if err != nil {
			return ctrl.Result{}, err
		}
		err = r.Create(ctx, configmap)
		if err != nil {
			ctxlog.Error(err, "Failed to create ConfigMap")
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// STEP 3: create the Job with the ConfigMap attached as a volume.
	job := &batchv1.Job{}
	err = r.Get(ctx, types.NamespacedName{Name: site.Name, Namespace: site.Namespace}, job)
	if err != nil && apierrors.IsNotFound(err) {

		ctxlog.Info("Creating new Job")
		configmapMode := int32(0554)
		job := &batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{
				Name:      site.Name,
				Namespace: site.Namespace,
			},
			Spec: batchv1.JobSpec{
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{"app": "sitedeployer"},
					},
					Spec: corev1.PodSpec{
						RestartPolicy: corev1.RestartPolicyNever,
						// STEP 3a: define the ConfigMap as a volume.
						Volumes: []corev1.Volume{
							{
								Name: "task-script-volume",
								VolumeSource: corev1.VolumeSource{
									ConfigMap: &corev1.ConfigMapVolumeSource{
										LocalObjectReference: corev1.LocalObjectReference{
											Name: "sitedeployer",
										},
										DefaultMode: &configmapMode,
									},
								},
							},
							{
								Name: "sitedeployersecrets",
								VolumeSource: corev1.VolumeSource{
									Secret: &corev1.SecretVolumeSource{
										SecretName: "sitedeployer",
									},
								},
							},
						},
						Containers: []corev1.Container{
							{
								Name:  "deploy",
								Image: "jmv2/deployrunner:0.0.13",
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
								Env: []corev1.EnvVar{
									{
										Name:  "CLUSTER_NAME",
										Value: site.Spec.ClusterName,
									},
									{
										Name:  "NODE_IMAGE",
										Value: site.Spec.NodeImage,
									},
								},
								// STEP 3b: mount the ConfigMap volume.
								VolumeMounts: []corev1.VolumeMount{
									{
										Name:      "task-script-volume",
										MountPath: "/scripts",
										ReadOnly:  true,
									},
									{
										Name:      "sitedeployersecrets",
										MountPath: "/etc/openstack/clouds.yaml",
										ReadOnly:  true,
										SubPath:   "clouds.yaml",
									},
									{
										Name:      "sitedeployersecrets",
										MountPath: "/etc/openstack/clouds-public.yaml",
										ReadOnly:  true,
										SubPath:   "clouds-public.yaml",
									},
									{
										Name:      "sitedeployersecrets",
										MountPath: "/deployer/admin.rc",
										ReadOnly:  true,
										SubPath:   "admin.rc",
									},
								},
								// STEP 3c: run the volume-mounted script.
								Command: []string{"/scripts/task.sh"},
							},
						},
					},
				},
			},
		}

		err = ctrl.SetControllerReference(site, job, r.Scheme)
		if err != nil {
			return ctrl.Result{}, err
		}
		err = r.Create(ctx, job)
		if err != nil {
			ctxlog.Error(err, "Failed to create Job")
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// Requeue if the job is not complete.
	if *job.Spec.Completions == 0 {
		ctxlog.Info("Requeuing to wait for Job to complete")
		return ctrl.Result{RequeueAfter: time.Second * 15}, nil
	}

	ctxlog.Info("Job Queued")
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *SiteReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&deployv1.Site{}).
		Complete(r)
}
