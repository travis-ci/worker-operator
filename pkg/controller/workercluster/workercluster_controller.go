package workercluster

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"time"

	travisciv1alpha1 "github.com/travis-ci/worker-operator/pkg/apis/travisci/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

var log = logf.Log.WithName("controller_workercluster")

// Add creates a new WorkerCluster Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &ReconcileWorkerCluster{client: mgr.GetClient(), scheme: mgr.GetScheme()}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("workercluster-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource WorkerCluster
	err = c.Watch(&source.Kind{Type: &travisciv1alpha1.WorkerCluster{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	// Watch for changes to secondary resource Deployments and requeue the owner WorkerCluster
	err = c.Watch(&source.Kind{Type: &appsv1.Deployment{}}, &handler.EnqueueRequestForOwner{
		IsController: true,
		OwnerType:    &travisciv1alpha1.WorkerCluster{},
	})
	if err != nil {
		return err
	}

	// Watch for changes to pods in worker clusters
	podMapper := &PodMapper{client: mgr.GetClient()}
	err = c.Watch(&source.Kind{Type: &corev1.Pod{}}, &handler.EnqueueRequestsFromMapFunc{
		ToRequests: podMapper,
	})
	if err != nil {
		return err
	}

	return nil
}

var _ reconcile.Reconciler = &ReconcileWorkerCluster{}

// ReconcileWorkerCluster reconciles a WorkerCluster object
type ReconcileWorkerCluster struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client client.Client
	scheme *runtime.Scheme
}

// Reconcile reads that state of the cluster for a WorkerCluster object and makes changes based on the state read
// and what is in the WorkerCluster.Spec
func (r *ReconcileWorkerCluster) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling WorkerCluster")

	// Fetch the WorkerCluster instance
	instance := &travisciv1alpha1.WorkerCluster{}
	err := r.client.Get(context.TODO(), request.NamespacedName, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	deployment := newDeploymentForCluster(instance)

	// Set WorkerCluster instance as the owner and controller
	if err := controllerutil.SetControllerReference(instance, deployment, r.scheme); err != nil {
		return reconcile.Result{}, err
	}

	// Check if this Deployment already exists
	found := &appsv1.Deployment{}
	err = r.client.Get(context.TODO(), types.NamespacedName{Name: deployment.Name, Namespace: deployment.Namespace}, found)
	if err != nil && errors.IsNotFound(err) {
		reqLogger.Info("Creating a new Deployment", "Deployment.Namespace", deployment.Namespace, "Deployment.Name", deployment.Name)
		err = r.client.Create(context.TODO(), deployment)
		if err != nil {
			reqLogger.Error(err, "Could not create deployment")
			return reconcile.Result{}, err
		}

		// Done. We'll come through here again in response to the deployment being created, and
		// that's where we will assign pool sizes.
		return reconcile.Result{}, nil
	} else if err != nil {
		return reconcile.Result{}, err
	}

	if deploymentNeedsUpdate(found, deployment) {
		reqLogger.Info("Updating deployment")

		if err = r.client.Update(context.TODO(), deployment); err != nil {
			reqLogger.Error(err, "Could not update deployment")
			return reconcile.Result{}, err
		}

		// That's all for now. The updated deployment will trigger another run through the loop.
		return reconcile.Result{}, nil
	}

	// List the pods for the deployment, and determine their pool sizes
	podList := &corev1.PodList{}
	labelSelector := labels.SelectorFromSet(found.Spec.Selector.MatchLabels)
	listOps := &client.ListOptions{Namespace: instance.Namespace, LabelSelector: labelSelector}
	if err = r.client.List(context.TODO(), listOps, podList); err != nil {
		return reconcile.Result{}, err
	}

	// Pass 1: Gather info from each worker on their current pool size
	statuses := make([]travisciv1alpha1.WorkerStatus, len(podList.Items))
	var terminatingJobs int32
	for i, pod := range podList.Items {
		s, err := getWorkerStatus(&pod)
		if err != nil {
			return reconcile.Result{}, err
		}

		if s.Phase == travisciv1alpha1.WorkerTerminating {
			terminatingJobs += s.CurrentPoolSize
		}

		statuses[i] = *s
	}

	// Pass 2: Determine the desired pool size for each worker based on probed status
	jobsToAssign := instance.Spec.MaxJobs - terminatingJobs
	workersToAssign := *found.Spec.Replicas
	var anyTerminating bool
	for i, status := range statuses {
		if status.Phase == travisciv1alpha1.WorkerTerminating {
			// if any worker is terminating, then the pool sizes are going to shift, and we need to check in and reconcile
			// to make sure we compensate for the lost capacity as the terminating worker drains its pool
			anyTerminating = true

			// Don't assign anything to terminating workers.
			continue
		}

		if workersToAssign < 1 {
			// We have more non-terminating pods than we have declared replicas for. That
			// shouldn't happen, but maybe it could. We'll just stop assigning work and hope
			// it resolves in another reconciliation.
			reqLogger.V(1).Info("More non-terminating workers than desired replicas")
			continue
		}

		jobs := int32(math.Round(float64(jobsToAssign) / float64(workersToAssign)))
		jobsToAssign -= jobs
		workersToAssign--

		// Only actually assign the jobs if the worker is running. We allocated jobs for
		// pending workers, expecting that they will become running soon.
		if status.Phase == travisciv1alpha1.WorkerRunning {
			statuses[i].RequestedPoolSize = jobs
		}
	}

	// Pass 3: Actually assign the requested pool sizes to the workers
	var changed bool
	for i, status := range statuses {
		var assigned bool
		if assigned, err = assignPoolSize(&podList.Items[i], status); err != nil {
			return reconcile.Result{}, err
		}

		changed = changed || assigned
	}

	instance.Status = travisciv1alpha1.WorkerClusterStatus{
		WorkerStatuses: statuses,
	}
	if err = r.client.Status().Update(context.TODO(), instance); err != nil {
		return reconcile.Result{}, err
	}

	result := reconcile.Result{}
	if anyTerminating || changed {
		// Check back in soon if we made any changes. Stop checking in once we go
		// through the loop without making any modifications.
		result.RequeueAfter = 10 * time.Second
	}

	return result, nil
}

func newDeploymentForCluster(cluster *travisciv1alpha1.WorkerCluster) *appsv1.Deployment {
	maxUnavailable := intstr.FromInt(0)
	maxSurge := intstr.FromInt(1)
	checkPort := intstr.FromInt(8080)

	replicas := int32(math.Ceil(float64(cluster.Spec.MaxJobs) / float64(cluster.Spec.MaxJobsPerWorker)))
	gracePeriod := int64(7200)
	defaultMode := int32(420)

	t := cluster.Spec.Template.DeepCopy()
	s := t.Spec

	var volumes []corev1.Volume
	container := corev1.Container{
		Name:            "worker",
		Image:           s.Image,
		ImagePullPolicy: s.ImagePullPolicy,
		Env:             configureEnvironment(s.Env),
		EnvFrom:         s.EnvFrom,
		LivenessProbe: &corev1.Probe{
			InitialDelaySeconds: 120,
			Handler: corev1.Handler{
				HTTPGet: &corev1.HTTPGetAction{
					Path: "/healthz",
					Port: checkPort,
				},
			},
		},
		ReadinessProbe: &corev1.Probe{
			Handler: corev1.Handler{
				HTTPGet: &corev1.HTTPGetAction{
					Path: "/ready",
					Port: checkPort,
				},
			},
		},
	}

	if s.SSHKeySecret != "" {
		volumes = []corev1.Volume{{
			Name: "travis-vm-ssh-key",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName:  s.SSHKeySecret,
					DefaultMode: &defaultMode,
				},
			},
		}}
		container.VolumeMounts = []corev1.VolumeMount{{
			Name:      "travis-vm-ssh-key",
			ReadOnly:  true,
			MountPath: "/etc/worker/ssh",
		}}
	}

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cluster.Name,
			Namespace: cluster.Namespace,
			Labels:    cluster.Labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: cluster.Spec.Selector,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: t.ObjectMeta,
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						container,
					},
					Volumes:                       volumes,
					TerminationGracePeriodSeconds: &gracePeriod,
				},
			},
			Strategy: appsv1.DeploymentStrategy{
				Type: appsv1.RollingUpdateDeploymentStrategyType,
				RollingUpdate: &appsv1.RollingUpdateDeployment{
					MaxUnavailable: &maxUnavailable,
					MaxSurge:       &maxSurge,
				},
			},
		},
	}
}

func configureEnvironment(env []corev1.EnvVar) []corev1.EnvVar {
	for i := range env {
		if env[i].ValueFrom != nil && env[i].ValueFrom.FieldRef != nil {
			if env[i].ValueFrom.FieldRef.APIVersion == "" {
				env[i].ValueFrom.FieldRef.APIVersion = "v1"
			}
		}
	}

	return append(env, additionalEnvVars()...)
}

func additionalEnvVars() []corev1.EnvVar {
	return []corev1.EnvVar{
		{
			// The remote controller API is used to adjust pool sizes on the fly
			Name:  "TRAVIS_WORKER_REMOTE_CONTROLLER_ADDR",
			Value: "0.0.0.0:8080",
		},
		{
			Name: "TRAVIS_WORKER_REMOTE_CONTROLLER_AUTH",
			// TODO make this randomly assigned.
			// The operator needs to know what this is to talk to the worker, but it will have the pod definition,
			// so it could just read it from there when it needs to query the worker API
			Value: "worker:worker",
		},
		{
			// Don't start any processors when the worker starts.
			// Instead, let this operator use the API to assign a pool size.
			Name:  "TRAVIS_WORKER_POOL_SIZE",
			Value: "0",
		},
	}
}

func deploymentNeedsUpdate(old, new *appsv1.Deployment) bool {
	depLogger := log.WithValues("Deployment.Name", new.Name)

	if *old.Spec.Replicas != *new.Spec.Replicas {
		depLogger.Info("update needed", "Old.Replicas", *old.Spec.Replicas, "New.Replicas", *new.Spec.Replicas)
		return true
	}

	os := &old.Spec.Template.Spec
	ns := &new.Spec.Template.Spec

	if len(os.Containers) != len(ns.Containers) {
		depLogger.Info("update needed", "Old.Containers", len(os.Containers), "New.Containers", len(ns.Containers))
		return true
	}

	oc := &os.Containers[0]
	nc := &ns.Containers[0]

	if oc.Image != nc.Image {
		depLogger.Info("update needed", "Old.Image", oc.Image, "New.Image", nc.Image)
		return true
	}

	if oc.ImagePullPolicy != nc.ImagePullPolicy {
		depLogger.Info("update needed", "Old.ImagePullPolicy", oc.ImagePullPolicy, "New.ImagePullPolicy", nc.ImagePullPolicy)
		return true
	}

	if !apiequality.Semantic.DeepEqual(oc.Env, nc.Env) {
		// this may log that the env count matches, but we're actually comparing contents.
		// don't want to log them because they may be sensitive values
		depLogger.Info("update needed", "Old.Env", len(oc.Env), "New.Env", len(nc.Env))
		return true
	}

	if !apiequality.Semantic.DeepEqual(oc.EnvFrom, nc.EnvFrom) {
		depLogger.Info("update needed", "Old.EnvFrom", oc.EnvFrom, "New.EnvFrom", nc.EnvFrom)
		return true
	}

	if !apiequality.Semantic.DeepEqual(oc.VolumeMounts, nc.VolumeMounts) {
		depLogger.Info("update needed", "Old.VolumeMounts", oc.VolumeMounts, "New.VolumeMounts", nc.VolumeMounts)
		return true
	}

	if !apiequality.Semantic.DeepEqual(os.Volumes, ns.Volumes) {
		depLogger.Info("update needed", "Old.Volumes", os.Volumes, "New.Volumes", ns.Volumes)
		return true
	}

	if oc.LivenessProbe.InitialDelaySeconds != nc.LivenessProbe.InitialDelaySeconds {
		depLogger.Info("update needed", "Old.LivenessProbe.InitialDelaySeconds", oc.LivenessProbe.InitialDelaySeconds, "New.LivenessProbe.InitialDelaySeconds", nc.LivenessProbe.InitialDelaySeconds)
		return true
	}

	depLogger.Info("already up-to-date")
	return false
}

func getWorkerStatus(pod *corev1.Pod) (*travisciv1alpha1.WorkerStatus, error) {
	s := &travisciv1alpha1.WorkerStatus{
		Name:  pod.Name,
		Phase: travisciv1alpha1.WorkerPending,
	}

	url := workerURL(pod)
	if url == "" {
		// We won't get anymore info yet
		return s, nil
	}
	if pod.Status.Phase != corev1.PodRunning {
		// Same
		return s, nil
	}

	// There isn't actually a pod phase that represents a pod that is terminating.
	// Instead, the presence of a deletion timestamp is the canonical indicator of this,
	// and is what prompts kubectl to show a pod as Terminating.
	if pod.DeletionTimestamp == nil {
		s.Phase = travisciv1alpha1.WorkerRunning
	} else {
		s.Phase = travisciv1alpha1.WorkerTerminating
	}

	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var workerInfo struct {
		PoolSize         int32 `json:"poolSize"`
		ExpectedPoolSize int32 `json:"expectedPoolSize"`
	}
	if err = json.NewDecoder(resp.Body).Decode(&workerInfo); err != nil {
		return nil, err
	}

	s.CurrentPoolSize = workerInfo.PoolSize
	s.ExpectedPoolSize = workerInfo.ExpectedPoolSize

	return s, nil
}

func assignPoolSize(pod *corev1.Pod, status travisciv1alpha1.WorkerStatus) (bool, error) {
	// We don't request empty pools. Either the pod is terminating, or it isn't ready to
	// be told its pool size yet.
	if status.RequestedPoolSize == 0 {
		return false, nil
	}

	// We've already told this pod the right pool size. Its current pool size may not match,
	// but it should in time.
	if status.RequestedPoolSize == status.ExpectedPoolSize {
		return false, nil
	}

	log.Info("Assigning pool size to worker pod", "Pod.Name", pod.Name, "PoolSize", status.RequestedPoolSize)

	var data struct {
		PoolSize int32 `json:"poolSize"`
	}
	data.PoolSize = status.RequestedPoolSize

	b := new(bytes.Buffer)
	json.NewEncoder(b).Encode(data)
	url := workerURL(pod)

	req, err := http.NewRequest(http.MethodPatch, url, b)
	if err != nil {
		return false, err
	}

	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return false, err
	}

	if resp.StatusCode >= 400 {
		return false, fmt.Errorf("unexpected status code %s", resp.Status)
	}

	return true, nil
}

func workerURL(pod *corev1.Pod) string {
	ip := pod.Status.PodIP
	if ip == "" {
		return ""
	}

	// TODO pull basic auth credentials from the pod
	return fmt.Sprintf("http://%s@%s:8080/worker", "worker:worker", ip)
}

// PodMapper creates reconcile requests for WorkerClusters based on changes to the
// pods that make up the cluster.
type PodMapper struct {
	client client.Client
}

// Map works back from a Pod to find the WorkerCluster it belongs to. Since this will
// run for all pods in the Kubernetes cluster, we may find pods that don't belong to
// a worker cluster, which we will ignore.
func (m *PodMapper) Map(i handler.MapObject) []reconcile.Request {
	if i.Meta == nil {
		return nil
	}

	podLogger := log.WithValues("Pod.Name", i.Meta.GetName(), "Pod.Namespace", i.Meta.GetNamespace())
	podLogger.Info("Attempting to map pod to worker cluster")

	// Step 1: Get the controlling ReplicaSet, if any
	ownerRef := metav1.GetControllerOf(i.Meta)
	if ownerRef == nil || ownerRef.Kind != "ReplicaSet" {
		// this pod does not have a controlling ReplicaSet, so ignore it
		return nil
	}

	// ownerRef does not have a namespace, but it should be the same as our pod's namespace
	name := types.NamespacedName{Namespace: i.Meta.GetNamespace(), Name: ownerRef.Name}

	rs := &appsv1.ReplicaSet{}
	if err := m.client.Get(context.TODO(), name, rs); err != nil {
		return nil
	}

	// Step 2: Get the controlling Deployment, if any
	ownerRef = metav1.GetControllerOf(rs)
	if ownerRef == nil || ownerRef.Kind != "Deployment" {
		// this replicaset does not have a controlling deployment, so ignore it
		return nil
	}

	name.Name = ownerRef.Name

	deployment := &appsv1.Deployment{}
	if err := m.client.Get(context.TODO(), name, deployment); err != nil {
		return nil
	}

	// Step 3: See if this deployment is controlled by a WorkerCluster. If so, return a
	// request to enqueue it. We don't need to actually fetch the cluster though.
	ownerRef = metav1.GetControllerOf(deployment)
	if ownerRef == nil || ownerRef.Kind != "WorkerCluster" {
		// this deployment does not have a controlling worker cluster, so ignore it
		return nil
	}

	name.Name = ownerRef.Name
	podLogger.Info("Found worker cluster for pod", "Cluster.Name", name.Name)

	return []reconcile.Request{
		{NamespacedName: name},
	}
}
