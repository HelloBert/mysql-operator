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
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	rbacv1 "k8s.io/api/rbac/v1"

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
	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-patcher",
			Namespace: ns,
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{""},
				Resources: []string{"pods"},
				Verbs:     []string{"patch", "get"},
			},
		},
	}

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

	// 2. 定义 RoleBinding，绑定到 default ServiceAccount
	binding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-patcher-binding",
			Namespace: ns,
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      "default",
				Namespace: ns,
			},
		},
		RoleRef: rbacv1.RoleRef{
			Kind:     "Role",
			Name:     "pod-patcher",
			APIGroup: "rbac.authorization.k8s.io",
		},
	}

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

	// ===================== 核心：statefulset 名称与命名空间 =====================
	statefulsetName := cluster.Name + "-statefulset"
	statefulsetKey := client.ObjectKey{Namespace: cluster.Namespace, Name: statefulsetName}

	//======================创建configmap=====================
	cmName := cluster.Name + "-mycnf"

	mycnfContent := `[mysqld]
gtid_mode=ON
enforce_gtid_consistency=ON
binlog_format=ROW
log_bin=mysql-bin
default_authentication_plugin = mysql_native_password
skip_name_resolve = 1

!includedir /etc/mysql/conf.d/
`

	mycnfCm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cmName,
			Namespace: cluster.Namespace,
			Labels:    map[string]string{"MysqlCluster": cluster.Name},
		},
		Data: map[string]string{
			"my.cnf": mycnfContent,
		},
	}

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
	bootstrapCmName := cluster.Name + "-bootstrap"
	bootstrapScript := `#!/bin/bash
	set -e
	#先判断mysql是否存活
	POD_INDEX=${HOSTNAME##*-}
	INIT_MARK_FILE="/var/lib/mysql/.init_done"

	wait_mysql() {
		while true;do
		if mysqladmin ping -uroot -p${MYSQL_ROOT_PASSWORD} -h127.0.0.1 --silent 2> /dev/null; then
			echo "数据库已正常启动"
			break
		else
			sleep 2
		fi
	done
	}
	
	main() {
		wait_mysql
		#判断标记文件是否存在，如果存在说明之前已经惊醒过初始化操作了，直接返回
		if [ -f $INIT_MARK_FILE ];then
			echo "该节点已经完成过初始化操作，无需创建标记文件"
			return 0
		fi

		echo "开始执行初始化逻辑，pod index:${POD_INDEX}"

		#判断是否是0节点，如果是0节点，就执行master初始化工作，如果时slave节点就执行slave初始化工作
		if [ $POD_INDEX -eq 0 ];then
			echo "当前是pod‑0，作为master，创建repl复制用户"
			#执行master初始化任务
			mysql -uroot -p${MYSQL_ROOT_PASSWORD} -h127.0.0.1 -e "CREATE USER IF NOT EXISTS 'repl'@'%' IDENTIFIED WITH mysql_native_password BY 'repl123';
			GRANT REPLICATION SLAVE ON *.* TO 'repl'@'%';
			set global server_id=$(( POD_INDEX+1 ));
			FLUSH PRIVILEGES;"
		else
			#执行slave初始化工作
			mysql -uroot -p"${MYSQL_ROOT_PASSWORD}" -h127.0.0.1 -e "STOP SLAVE;
			RESET SLAVE ALL;
			set global server_id=$(( POD_INDEX+1 ));
			CHANGE MASTER TO MASTER_HOST='${HEADLESS_SERVICE}', 
			MASTER_USER='repl', 
			MASTER_PASSWORD='repl123', 
			MASTER_AUTO_POSITION=1;
			START SLAVE;"
		fi
		#创建标记文件
		touch $INIT_MARK_FILE
		echo "初始化完成，写入标记文件 ${INIT_MARK_FILE}"
	}
	main
	TOKEN=$(cat /var/run/secrets/kubernetes.io/serviceaccount/token)
	API_SERVER="https://kubernetes.default.svc"
	while true;do
		if mysqladmin ping -h127.0.0.1 -uroot -p${MYSQL_ROOT_PASSWORD} --silent &> /dev/null;then
			HEALTHY="true"
		else
			HEALTHY="false"
		fi
		curl -s -k -X PATCH \
  		-H "Authorization: Bearer $TOKEN" \
  		-H "Content-Type: application/merge-patch+json" \
  		$API_SERVER/api/v1/namespaces/$POD_NAMESPACE/pods/$HOSTNAME \
  		--data "{\"metadata\":{\"annotations\":{\"mysql-health\":\"$HEALTHY\"}}}" > /dev/null 2>&1
		sleep 30
	done`

	bootstraptCm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      bootstrapCmName,
			Namespace: cluster.Namespace,
			Labels:    map[string]string{"MysqlCluster": cluster.Name},
		},
		Data: map[string]string{
			"bootstrap.sh": bootstrapScript,
		},
	}

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
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceName,
			Namespace: cluster.Namespace,
			Labels:    map[string]string{"MysqlCluster": cluster.Name},
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: "None",
			Selector:  map[string]string{"MysqlCluster": cluster.Name},
			Ports: []corev1.ServicePort{
				{
					Port:       3306,
					TargetPort: intstr.FromInt(3306),
				},
			},
		},
	}
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

	// 2. 检查 Deployment 是否存在
	var existingSts appsv1.StatefulSet
	err := r.Get(ctx, statefulsetKey, &existingSts)

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

	// ===================== 3. Statefulset 不存在 → 创建 =====================
	if err != nil && errors.IsNotFound(err) {
		logger.Info("Statefulset 不存在，开始创建", "name", statefulsetName)

		sts := &appsv1.StatefulSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      statefulsetName,
				Namespace: cluster.Namespace,
				Labels:    map[string]string{"MysqlCluster": cluster.Name},
			},
			Spec: appsv1.StatefulSetSpec{
				ServiceName: cluster.Name + "-service",
				Replicas:    &cluster.Spec.Replicas,
				Selector: &metav1.LabelSelector{
					MatchLabels: map[string]string{"MysqlCluster": cluster.Name},
				},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{"MysqlCluster": cluster.Name},
					},
					Spec: corev1.PodSpec{
						//=================containerInit容器==================
						InitContainers: []corev1.Container{
							{
								Name:    "gen-mysql-cnf",
								Image:   cluster.Spec.Image,
								Command: []string{"/bin/sh", "-c"},
								Args: []string{`POD_INDEX=${HOSTNAME##*-}
SERVER_ID=$((POD_INDEX+1))

cat > /output/server-id.cnf <<EOF
[mysqld]
server-id=${SERVER_ID}
EOF
echo "server-id已生成: ${SERVER_ID}"`},
								VolumeMounts: []corev1.VolumeMount{
									{
										Name:      "cnf-override",
										MountPath: "/output",
									},
								},
							},
						},
						Containers: []corev1.Container{
							//===============mysql容器==================
							{
								Name:            "mysql",
								Image:           cluster.Spec.Image,
								ImagePullPolicy: cluster.Spec.ImagePullPolicy,
								Ports: []corev1.ContainerPort{{
									ContainerPort: cluster.Spec.Port,
								}},

								Env: envVars,
								VolumeMounts: []corev1.VolumeMount{
									{
										Name:      "data",
										MountPath: "/var/lib/mysql",
									},
									{
										Name:      "mycnf",
										MountPath: "/etc/mysql/my.cnf",
										SubPath:   "my.cnf",
									},
									{
										Name:      "cnf-override",
										MountPath: "/etc/mysql/conf.d",
									},
								},
							},
							//==============sidecar容器=================
							{
								//容器名称是什么
								Name:  "sidecar-health",
								Image: "registry.cn-hangzhou.aliyuncs.com/lpx03/mysql:8.0",

								//启动命令是什么
								Command: []string{"/bin/bash"},
								Args:    []string{`/opt/bootstrap/bootstrap.sh`},
								//设置环境变量，以供下面的shell脚本使用

								Env: []corev1.EnvVar{
									{
										Name: "POD_NAMESPACE",
										ValueFrom: &corev1.EnvVarSource{
											FieldRef: &corev1.ObjectFieldSelector{
												FieldPath: "metadata.namespace",
											},
										},
									},
									{
										Name:  "MYSQL_ROOT_PASSWORD",
										Value: mysqlPassword,
									},
									{
										Name:  "HEADLESS_SERVICE",
										Value: statefulsetName + "-0." + serviceName,
									},
								},
								VolumeMounts: []corev1.VolumeMount{
									{
										Name:      "bootstrap",
										MountPath: "/opt/bootstrap",
									},
									{
										Name:      "data",
										MountPath: "/var/lib/mysql",
									},
								},
							},
						},
						Volumes: []corev1.Volume{
							{
								Name: "mycnf",
								VolumeSource: corev1.VolumeSource{
									ConfigMap: &corev1.ConfigMapVolumeSource{
										LocalObjectReference: corev1.LocalObjectReference{Name: cmName},
									},
								},
							},
							{
								Name: "bootstrap",
								VolumeSource: corev1.VolumeSource{
									ConfigMap: &corev1.ConfigMapVolumeSource{
										LocalObjectReference: corev1.LocalObjectReference{Name: bootstrapCmName},
									},
								},
							},
							{
								Name: "cnf-override",
								VolumeSource: corev1.VolumeSource{
									EmptyDir: &corev1.EmptyDirVolumeSource{},
								},
							},
						},
					},
				},
				VolumeClaimTemplates: []corev1.PersistentVolumeClaim{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "data",
							Namespace: cluster.Namespace,
							Labels:    map[string]string{"app": "mysql", "cluster": cluster.Name},
						},
						Spec: corev1.PersistentVolumeClaimSpec{
							StorageClassName: &cluster.Spec.StorageClassName,
							AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
							Resources: corev1.VolumeResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceStorage: resource.MustParse("10Gi"),
								},
							},
						},
					},
				},
			},
		}

		// 设置 OwnerReference（级联删除）
		if err := controllerutil.SetControllerReference(&cluster, sts, r.Scheme); err != nil {
			logger.Error(err, "设置 OwnerReference 失败")
			return ctrl.Result{}, err
		}

		// 创建 Statefulset
		if err := r.Create(ctx, sts); err != nil {
			logger.Error(err, "创建 Statefulset 失败")
			return ctrl.Result{}, err
		}

		logger.Info("Statefulset 创建成功", "name", statefulsetName)
		// 创建后重新入队，立即同步状态
		return ctrl.Result{RequeueAfter: 1 * time.Second}, nil
	}
	// ===================== 4. 获取 statefulset 失败（非 404） =====================
	if err != nil {
		logger.Error(err, "获取 Statefulset 失败")
		return ctrl.Result{}, err
	}

	// ===================== 5. statefulset 已存在 → 同步状态 =====================
	// 读取真实就绪副本数
	cluster.Status.ReadyReplicas = existingSts.Status.ReadyReplicas
	cluster.Status.StatefulSetName = statefulsetName
	cluster.Status.LastUpdated = time.Now().Format(time.RFC3339)

	if cluster.Status.ReadyReplicas > 0 {
		cluster.Status.Phase = "Running"
		cluster.Status.Message = "Deployment is ready"
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
