#!/bin/bash

. admin.rc

function get_token() {
  curl -D - -s "${DU_URL}"/keystone/v3/auth/tokens \
    -H "Content-Type: application/json" \
    -d '{"auth":{"identity":{"methods":["password"],"password":{"user":{"name":"'$ADMIN_USER'","domain":{"name": "Default"},"password":"'$ADMIN_PASS'"}}},"scope":{"project":{"domain": {"name": "Default"},"name":"service"}}}}' | grep -iE '^X-Subject-Token' | awk '{print $2}'
}

function get_project_id() {
curl -s "${DU_URL}"/keystone/v3/projects \
  -X GET \
  -H "X-Auth-Token: $1" \
  | jq -r '.projects[]| select(.name=="service") | .id'
}

function get_nodepool_id() {
curl -s "${DU_URL}/qbert/v4/$2/nodePools" \
  -X GET \
  -H "X-Auth-Token: $1" \
  | jq -r '.[]| select(.name=="defaultPool") | .uuid'
}

function get_role_version() {
curl -s "${DU_URL}/qbert/v4/$2/clusters/supportedRoleVersions" \
  -X GET \
  -H "X-Auth-Token: $1" \
  | jq -r '.roles[length -1] | .roleVersion' # latest role
}

function get_clusters() {
curl -s "${DU_URL}/qbert/v4/$3/clusters" \
  --http1.1 \
  -H "X-Auth-Token: $2" \
  -H "Cookie: X-Auth-Token: $2" \
  | jq -r ".[] | select(.name == \"$1\") | .uuid"
}

function create_cluster() {
curl -s "${DU_URL}/qbert/v4/$3/clusters" \
  --http1.1 \
  -H "X-Auth-Token: $2" \
  -H "Cookie: X-Auth-Token: $2" \
  -H "Content-Type: application/json" \
  -d @- << EOF
{
  "name": "$1",
  "allowWorkloadsOnMaster": true,
  "containersCidr": "10.20.0.0/16",
  "servicesCidr": "10.21.0.0/16",
  "mtuSize": 1440,
  "privileged": true,
  "appCatalogEnabled": false,
  "deployLuigiOperator": false,
  "useHostname": false,
  "nodePoolUuid": "$4",
  "kubeRoleVersion": "$5",
  "calicoIpIpMode": "Always",
  "calicoNatOutgoing": true,
  "calicoV4BlockSize": "26",
  "calicoIPv4DetectionMethod": "first-found",
  "networkPlugin": "calico",
  "runtimeConfig": "",
  "numMasters": 1,
  "numWorkers": 0,
  "containerRuntime": "containerd",
  "etcdBackup": {
    "storageType": "local",
    "isEtcdBackupEnabled": 1,
    "storageProperties": {
      "localPath": "/etc/pf9/etcd-backup"
    },
    "intervalInMins": 59,
    "maxIntervalBackupCount": 24
  },
  "tags": {}
}
EOF
}

token=$(get_token)
project_id=$(get_project_id "$token")
# check if a cluster with this name already exists
cluster_uuid=$(get_clusters "$1" "$token" "$project_id")
if [ "$cluster_uuid" != "" ]; then
  echo "$cluster_uuid"
  exit
fi

role_version=$(get_role_version "$token" "$project_id")
node_pool_id=$(get_nodepool_id "$token" "$project_id")
cluster_uuid=$(create_cluster "$1" "$token" "$project_id" "$node_pool_id" "$role_version")
echo "$cluster_uuid" | jq -r .uuid
