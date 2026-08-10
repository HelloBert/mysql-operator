package mysqlcluster

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	myappv1 "mysql-operator/api/v1"
)

func NewMysqlStatefulSet(
	statefulsetName string,
	serviceName string,
	cluster *myappv1.MySQLCluster,
	replicas int32,
	envVars []corev1.EnvVar,
	mysqlPassword string,
	masterPod string,
) *appsv1.StatefulSet {

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
								{
									Name:  "MY_MASTER_POD",
									Value: masterPod,
								},
								{
									Name:  "SERVICE_NAME",
									Value: serviceName, //mysql-service
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
									//这里和configmap的cmName保持一致
									LocalObjectReference: corev1.LocalObjectReference{Name: cluster.Name + "-mycnf"},
								},
							},
						},
						{
							Name: "bootstrap",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{Name: cluster.Name + "-bootstrap"},
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
	return sts
}
