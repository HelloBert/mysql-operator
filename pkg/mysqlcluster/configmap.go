package mysqlcluster

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	myappv1 "mysql-operator/api/v1"
)

func NewMyCnfConfigMap(cluster *myappv1.MySQLCluster) *corev1.ConfigMap {
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

	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cmName,
			Namespace: cluster.Namespace,
			Labels:    map[string]string{"MysqlCluster": cluster.Name},
		},
		Data: map[string]string{
			"my.cnf": mycnfContent,
		},
	}

}

func NewBootstrapConfigMap(cluster *myappv1.MySQLCluster) *corev1.ConfigMap {
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
	
	wait_master_env(){
		echo "等待my_master_pod环境变量"
		while [ -z "${MY_MASTER_POD}" ];do
			sleep 2
		done
		echo "获取到MY_MASTER_POD,${MY_MASTER_POD}"
	}

	main() {
		wait_mysql
		wait_master_env

		#判断标记文件是否存在，如果存在说明之前已经惊醒过初始化操作了，直接返回
		if [ -f $INIT_MARK_FILE ];then
			echo "该节点已经完成过初始化操作，无需创建标记文件"
			return 0
		fi

		echo "开始执行初始化逻辑，pod index:${POD_INDEX}"

		#判断是否是0节点，如果是0节点，就执行master初始化工作，如果时slave节点就执行slave初始化工作
		if [ "${HOSTNAME}" = "${MY_MASTER_POD}" ];then
			echo "当前是节点是集群主节点，作为master，创建repl复制用户"
			#执行master初始化任务
			mysql -uroot -p${MYSQL_ROOT_PASSWORD} -h127.0.0.1 -e "CREATE USER IF NOT EXISTS 'repl'@'%' IDENTIFIED WITH mysql_native_password BY 'repl123';
			GRANT REPLICATION SLAVE ON *.* TO 'repl'@'%';
			set global server_id=$(( POD_INDEX+1 ));
			FLUSH PRIVILEGES;"
		else
			#执行slave初始化工作
			echo "当前节点是从节点，指向主节点${MY_MASTER_POD}.${SERVICE_NAME}"
			mysql -uroot -p"${MYSQL_ROOT_PASSWORD}" -h127.0.0.1 -e "STOP SLAVE;
			RESET SLAVE ALL;
			set global server_id=$(( POD_INDEX+1 ));
			CHANGE MASTER TO MASTER_HOST='${MY_MASTER_POD}.${SERVICE_NAME}', 
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

	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      bootstrapCmName,
			Namespace: cluster.Namespace,
			Labels:    map[string]string{"MysqlCluster": cluster.Name},
		},
		Data: map[string]string{
			"bootstrap.sh": bootstrapScript,
		},
	}

}
