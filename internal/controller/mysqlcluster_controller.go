/*
Copyright 2026.

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
	"mysql-operator/pkg/mysqlcluster"
	"sort"
	"strconv"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	myappv1 "mysql-operator/api/v1"
)

// MySQLClusterReconciler reconciles a MySQLCluster object
type MySQLClusterReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=myapp.myapp.io,resources=mysqlclusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=myapp.myapp.io,resources=mysqlclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=myapp.myapp.io,resources=mysqlclusters/finalizers,verbs=update
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;patch
// +kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the MySQLCluster object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.23.3/pkg/reconcile
func (r *MySQLClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = logf.FromContext(ctx)
	logger := log.FromContext(ctx)
	// 1. 获取自定义资源 App
	var cluster myappv1.MySQLCluster
	if err := r.Get(ctx, req.NamespacedName, &cluster); err != nil {
		if errors.IsNotFound(err) {
			// App 被删除，直接退出
			return ctrl.Result{}, nil
		}
		logger.Error(err, "获取 App 资源失败")
		return ctrl.Result{}, err
	}

	// ========== Finalizer 逻辑 ==========
	finalizerName := "myapp.myapp.io/mysqlcluster-finalizer"

	// 检查 App 是否正在被删除
	if !cluster.ObjectMeta.DeletionTimestamp.IsZero() {
		logger.Info("App 正在被删除，执行清理逻辑", "name", cluster.Name)

		// 执行清理逻辑
		if err := r.cleanupResources(ctx, &cluster); err != nil {
			logger.Error(err, "清理资源失败")
			return ctrl.Result{}, err
		}

		// 移除 Finalizer
		controllerutil.RemoveFinalizer(&cluster, finalizerName)
		if err := r.Update(ctx, &cluster); err != nil {
			logger.Error(err, "移除 Finalizer 失败")
			return ctrl.Result{}, err
		}

		logger.Info("清理完成，App 已删除", "name", cluster.Name)
		return ctrl.Result{}, nil
	}

	// 确保 Finalizer 存在（正常运行时）
	if !controllerutil.ContainsFinalizer(&cluster, finalizerName) {
		controllerutil.AddFinalizer(&cluster, finalizerName)
		if err := r.Update(ctx, &cluster); err != nil {
			logger.Error(err, "添加 Finalizer 失败")
			return ctrl.Result{}, err
		}
		logger.Info("已添加 Finalizer", "name", cluster.Name)
		// 重新入队，让后续逻辑继续
		return ctrl.Result{Requeue: true}, nil
	}

	logger.Info("开始 reconcile App", "name", cluster.Name, "image", cluster.Spec.Image)

	//创建并绑定权限
	ns := cluster.Namespace

	// 1. 定义 Role
	role := mysqlcluster.NewRbac(&cluster, ns)

	// 尝试创建 Role，如果已存在则更新
	if err := r.Create(ctx, role); err != nil {
		if !errors.IsAlreadyExists(err) {
			logger.Error(err, "创建 Role 失败", "namespace", ns)
			return ctrl.Result{}, err
		}
		// 如果已存在，更新它（防止规则变化）
		if err := r.Update(ctx, role); err != nil {
			logger.Error(err, "更新 Role 失败", "namespace", ns)
			return ctrl.Result{}, err
		}
	}
	binding := mysqlcluster.NewRbac(&cluster, ns)
	// 尝试创建 RoleBinding
	if err := r.Create(ctx, binding); err != nil {
		if !errors.IsAlreadyExists(err) {
			logger.Error(err, "创建 RoleBinding 失败", "namespace", ns)
			return ctrl.Result{}, err
		}
		// 如果已存在，更新它（确保绑定正确）
		if err := r.Update(ctx, binding); err != nil {
			logger.Error(err, "更新 RoleBinding 失败", "namespace", ns)
			return ctrl.Result{}, err
		}
	}

	logger.Info("权限配置已就绪", "namespace", ns)

	//======================创建configmap=====================
	mycnfCm := mysqlcluster.NewMyCnfConfigMap(&cluster)

	//级联删除，删除app时自动删除configmap
	if err := controllerutil.SetControllerReference(&cluster, mycnfCm, r.Scheme); err != nil {
		logger.Error(err, "设置configmap OwnerReference失败")
		return ctrl.Result{}, err
	}

	//创建configmap，如果存在就跳过
	if err := r.Create(ctx, mycnfCm); err != nil && !errors.IsAlreadyExists(err) {
		logger.Error(err, "创建cm失败")
		return ctrl.Result{}, err
	}

	//==================创建configmap-bootstrap脚本=====================
	bootstraptCm := mysqlcluster.NewBootstrapConfigMap(&cluster)

	//级联删除，删除app时自动删除configmap
	if err := controllerutil.SetControllerReference(&cluster, bootstraptCm, r.Scheme); err != nil {
		logger.Error(err, "设置configmap OwnerReference失败")
		return ctrl.Result{}, err
	}

	//创建configmap，如果存在就跳过
	if err := r.Create(ctx, bootstraptCm); err != nil && !errors.IsAlreadyExists(err) {
		logger.Error(err, "创建cm失败")
		return ctrl.Result{}, err
	}

	// ===================== 创建 Service =====================
	serviceName := cluster.Name + "-service"
	service := mysqlcluster.NewHeadLesService(&cluster)
	// 设置 OwnerReference（删除 App 时自动删除 Service）
	if err := controllerutil.SetControllerReference(&cluster, service, r.Scheme); err != nil {
		logger.Error(err, "设置 Service OwnerReference 失败")
		return ctrl.Result{}, err
	}

	// 创建 Service（如果已存在则跳过）
	if err := r.Create(ctx, service); err != nil && !errors.IsAlreadyExists(err) {
		logger.Error(err, "创建 Service 失败")
		return ctrl.Result{}, err
	}

	//==================查询集群所有的pod，标签过滤=================
	podList := &corev1.PodList{}
	listOpt := []client.ListOption{
		client.InNamespace(cluster.Namespace),
		client.MatchingLabels(map[string]string{"MysqlCluster": cluster.Name}),
	}
	if err := r.List(ctx, podList, listOpt...); err != nil {
		return ctrl.Result{}, err
	}

	//pod排序：按后缀数字升序，0优先
	sort.Slice(podList.Items, func(i, j int) bool {
		nameA := podList.Items[i].Name
		nameB := podList.Items[j].Name

		idxStrA := nameA[strings.LastIndex(nameA, "-")+1:]
		idxStrB := nameB[strings.LastIndex(nameB, "-")+1:]

		ia, _ := strconv.Atoi(idxStrA)
		ib, _ := strconv.Atoi(idxStrB)
		return ia < ib
	})
	var selectedMasterPod string
	if len(podList.Items) > 0 {
		selectedMasterPod = podList.Items[0].Name
	}
	// 如果选出的master和status保存的不一样，更新CR status
	if cluster.Status.MasterPod != selectedMasterPod {
		cluster.Status.MasterPod = selectedMasterPod
		if err := r.Status().Update(ctx, &cluster); err != nil {
			return ctrl.Result{}, err
		}
		//statue更新触发时间，直接返回，让下一轮Reconcile执行sts更新
		return ctrl.Result{Requeue: true}, nil
	}

	// 2. 检查 Statefulset 是否存在

	// 处理环境变量
	envVars := make([]corev1.EnvVar, 0, len(cluster.Spec.Env))
	envVars = append(envVars, corev1.EnvVar{Name: "APP_VERSION", Value: "v1.0"})
	for k, v := range cluster.Spec.Env {
		envVars = append(envVars, corev1.EnvVar{Name: k, Value: v})
	}

	//读取环境变量中的root密码，给到sidecar容器
	mysqlPassword := "Ab123456" // 默认值
	for _, env := range envVars {
		if env.Name == "MYSQL_ROOT_PASSWORD" {
			mysqlPassword = env.Value
			break
		}
	}

	// ===================== 核心：statefulset 名称与命名空间 =====================
	// saName := cluster.Name + "-sa"
	statefulsetName := cluster.Name + "-statefulset"
	// statefulsetKey := client.ObjectKey{Namespace: cluster.Namespace, Name: statefulsetName}

	stsKey := types.NamespacedName{
		Name:      statefulsetName,
		Namespace: cluster.Namespace,
	}
	var existingSts appsv1.StatefulSet

	err := r.Get(ctx, stsKey, &existingSts)
	if err != nil {
		if !errors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
		// sts不存在，执行创建
		sts := mysqlcluster.NewMysqlStatefulSet(
			statefulsetName,
			serviceName,
			// saName,
			&cluster,
			cluster.Spec.Replicas,
			envVars,
			mysqlPassword,
			cluster.Status.MasterPod,
		)
		if err := controllerutil.SetControllerReference(&cluster, sts, r.Scheme); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.Create(ctx, sts); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// sts已经存在，执行update
	desiredSts := mysqlcluster.NewMysqlStatefulSet(
		statefulsetName,
		serviceName,
		// saName,
		&cluster,
		cluster.Spec.Replicas,
		envVars,
		mysqlPassword,
		cluster.Status.MasterPod,
	)
	// ⭐重点：只替换spec，保留metadata（resourceVersion不能丢！）
	existingSts.Spec = desiredSts.Spec
	if err := r.Update(ctx, &existingSts); err != nil {
		return ctrl.Result{}, err
	}

	// ===================== 5. statefulset 已存在 → 同步状态 =====================
	// 读取真实就绪副本数
	cluster.Status.ReadyReplicas = existingSts.Status.ReadyReplicas
	cluster.Status.StatefulSetName = statefulsetName
	cluster.Status.LastUpdated = time.Now().Format(time.RFC3339)

	if cluster.Status.ReadyReplicas > 0 {
		cluster.Status.Phase = "Running"
		cluster.Status.Message = "Statefulset is ready"
	} else {
		cluster.Status.Phase = "Pending"
		cluster.Status.Message = "Waiting for pods to be ready"
	}

	// 更新 App 状态（只更新一次）
	if err := r.Status().Update(ctx, &cluster); err != nil {
		logger.Error(err, "更新 App 状态失败")
		return ctrl.Result{}, err
	}

	if cluster.Status.Phase == "Degraded" {
		logger.Info("⚠️ 警告：App 处于降级状态",
			"name", cluster.Name,
			"ready", cluster.Status.ReadyReplicas,
			"expected", cluster.Spec.Replicas)
	}

	if cluster.Status.Phase == "pending" {
		logger.Info("警告: app正在等待启动", cluster.Name)

	}

	logger.Info("App reconcile 完成", "name", cluster.Name, "readyReplicas", cluster.Status.ReadyReplicas)
	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil

	// TODO(user): your logic here

	// return ctrl.Result{}, nil
}

// cleanupResources 执行删除前的清理工作
func (r *MySQLClusterReconciler) cleanupResources(ctx context.Context, cluster *myappv1.MySQLCluster) error {
	logger := log.FromContext(ctx)
	logger.Info("🧹 开始清理资源", "mysqlcluster", cluster.Name)

	// ====== 在这里添加你的清理逻辑 ======

	// 示例 1：发送通知
	logger.Info("📢 发送删除通知", "app", cluster.Name)

	// 删除所有 PVC（按标签）
	pvcList := &corev1.PersistentVolumeClaimList{}
	if err := r.List(ctx, pvcList, client.InNamespace(cluster.Namespace),
		client.MatchingLabels(map[string]string{"app": "mysql", "cluster": cluster.Name})); err != nil {
		logger.Error(err, "获取 PVC 列表失败")
		return err
	}

	for _, pvc := range pvcList.Items {
		logger.Info("删除 PVC", "pvc", pvc.Name)
		if err := r.Delete(ctx, &pvc); err != nil && !errors.IsNotFound(err) {
			logger.Error(err, "删除 PVC 失败", "pvc", pvc.Name)
			return err
		}
	}

	//删除comfigmap
	cmName := cluster.Name + "-mycnf"
	configmap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cmName,
			Namespace: cluster.Namespace,
		},
	}
	if err := r.Delete(ctx, configmap); err != nil && !errors.IsNotFound(err) {
		return err
	}
	//删除service
	serviceName := cluster.Name + "-service"
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceName,
			Namespace: cluster.Namespace,
		},
	}
	if err := r.Delete(ctx, service); err != nil && !errors.IsNotFound(err) {
		return err
	}

	//删除secret
	secretName := cluster.Name + "secret"
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: cluster.Namespace,
		},
	}
	if err := r.Delete(ctx, secret); err != nil && !errors.IsNotFound(err) {
		return err
	}

	// ==================================

	logger.Info("✅ 清理完成", "app", cluster.Name)
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *MySQLClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&myappv1.MySQLCluster{}).
		Owns(&appsv1.StatefulSet{}). // 监听 Deployment 变化，自动触发 reconcile
		Owns(&corev1.ConfigMap{}).   //监听configmap变化，自动触发reconcile
		Named("mysqlcluster").
		Complete(r)
}
