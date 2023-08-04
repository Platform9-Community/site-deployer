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
    | jq -r '.projects[]| select(.name=="service") | .id' || echo -n "error"
}

function get_nodepool_id() {
  curl -s "${DU_URL}/qbert/v4/$2/nodePools" \
    -X GET \
    -H "X-Auth-Token: $1" \
    | jq -r '.[]| select(.name=="defaultPool") | .uuid' || echo -n "error"
}

function get_role_version() {
  curl -s "${DU_URL}/qbert/v4/$2/clusters/supportedRoleVersions" \
    -X GET \
    -H "X-Auth-Token: $1" \
    | jq -r '.roles[length -1] | .roleVersion' || echo -n "error"
}

function get_clusters() {
  curl -s "${DU_URL}/qbert/v4/$3/clusters" \
    --http1.1 \
    -H "X-Auth-Token: $2" \
    -H "Cookie: X-Auth-Token: $2" \
    | jq -r ".[] | select(.name == \"$1\") | .uuid" || echo -n "error"
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

function get_kubeconfig() {
  curl -s "${DU_URL}/qbert/v4/$2/kubeconfig/$3" \
    -X GET \
    -H "X-Auth-Token: $1" | sed -e "s/__INSERT_BEARER_TOKEN_HERE__/$1/"
}

token=$(get_token)
if [ "$(echo -n "$token" | wc -c)" -lt 5 ] || [ "$token" == "error" ]; then
  sleep 5
  token=$(get_token)
fi

project_id=$(get_project_id "$token")
if [ "$(echo -n "$project_id" | wc -c)" -lt 5 ] || [ "$project_id" == "error" ]; then
  sleep 5
  project_id=$(get_project_id "$token")
fi

# check if a cluster with this name already exists
cluster_uuid=$(get_clusters "$1" "$token" "$project_id")
if [ "$(echo -n "$cluster_uuid" | wc -c)" -gt 5 ] || [ "$cluster_uuid" == "error" ]; then
  echo "$cluster_uuid"
  exit 0
fi

role_version=$(get_role_version "$token" "$project_id")
if [ "$(echo -n "$role_version" | wc -c)" -lt 5 ] || [ "$role_version" == "error" ]; then
  sleep 5
  role_version=$(get_role_version "$token" "$project_id")
fi

node_pool_id=$(get_nodepool_id "$token" "$project_id")
if [ "$(echo -n "$node_pool_id" | wc -c)" -lt 5 ] || [ "$node_pool_id" == "error" ]; then
  sleep 5
  node_pool_id=$(get_nodepool_id "$token" "$project_id")
fi

cluster_uuid=$(create_cluster "$1" "$token" "$project_id" "$node_pool_id" "$role_version")
if [ "$(echo -n "$cluster_uuid" | wc -c)" -lt 5 ] || [ "$cluster_uuid" == "error" ]; then
  sleep 5
  cluster_uuid=$(create_cluster "$1" "$token" "$project_id" "$node_pool_id" "$role_version")
fi

if [ "$(echo -n "$cluster_uuid" | wc -c)" -lt 5 ] || [ "$cluster_uuid" == "error" ]; then
  echo "Something went wrong with cluster creation via Qbert API"
  exit 1
fi

>/tmp/cluster_uuid.txt

echo "$cluster_uuid" | jq -r .uuid | tee > /tmp/cluster_uuid.txt
