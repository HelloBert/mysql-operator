package mysqlcluster

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	myappv1 "mysql-operator/api/v1"
)

func NewHeadLesService(cluster *myappv1.MySQLCluster) *corev1.Service {
	serviceName := cluster.Name + "-service"
	return &corev1.Service{
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
}
