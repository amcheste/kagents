# Install on Amazon EKS

This guide walks you from a working EKS cluster to a running kagents operator backed by Amazon EFS for the ReadWriteMany storage requirement.

## Prerequisites

- An EKS cluster on Kubernetes 1.28+
- `kubectl` configured against the cluster
- `helm` 3.14+
- `aws` CLI authenticated with permissions to create EFS file systems and IAM policies
- The cluster's VPC ID and the security group used by your worker nodes. `aws eks describe-cluster --name <cluster-name>` shows them

## 1. Install the EFS CSI driver

The official AWS EFS CSI driver supports the `ReadWriteMany` access mode kagents requires. Install via the EKS add-on:

```bash
aws eks create-addon \
  --cluster-name <cluster-name> \
  --addon-name aws-efs-csi-driver \
  --resolve-conflicts OVERWRITE
```

Or via Helm if you prefer the upstream chart:

```bash
helm repo add aws-efs-csi-driver https://kubernetes-sigs.github.io/aws-efs-csi-driver/
helm install aws-efs-csi-driver aws-efs-csi-driver/aws-efs-csi-driver \
  --namespace kube-system
```

Verify pods are ready:

```bash
kubectl get pods -n kube-system -l app.kubernetes.io/name=aws-efs-csi-driver
```

## 2. Create the EFS file system

```bash
aws efs create-file-system \
  --creation-token kagents-state \
  --performance-mode generalPurpose \
  --throughput-mode elastic \
  --encrypted \
  --tags Key=Name,Value=kagents-state
```

Note the returned `FileSystemId` (looks like `fs-0abc123def456`).

Add a mount target in each worker subnet so pods on any node can mount it:

```bash
# For each subnet your nodes live in:
aws efs create-mount-target \
  --file-system-id fs-0abc123def456 \
  --subnet-id subnet-... \
  --security-groups sg-...     # the worker node security group
```

The security group must allow inbound NFS (TCP 2049) from itself.

## 3. Create the StorageClass

```yaml title="storageclass-efs.yaml"
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  # The operator defaults to a class named "nfs"; using that name avoids
  # needing to override storage.storageClassName in the chart values.
  name: nfs
provisioner: efs.csi.aws.com
parameters:
  provisioningMode: efs-ap
  fileSystemId: fs-0abc123def456
  directoryPerms: "700"
  uid: "65532"
  gid: "65532"
reclaimPolicy: Retain
volumeBindingMode: Immediate
```

Apply it:

```bash
kubectl apply -f storageclass-efs.yaml
```

The `efs-ap` provisioning mode creates an EFS Access Point per PVC, which gives each team its own permissioned root directory inside the shared file system.

## 4. Install kagents

```bash
helm install kagents \
  oci://ghcr.io/amcheste/charts/kagents \
  --namespace claude-teams-system --create-namespace
```

Wait for the operator to be ready:

```bash
kubectl rollout status deployment/kagents-controller-manager \
  --namespace claude-teams-system --timeout=120s
```

## 5. Verify with the mailbox smoke test

The repo includes a smoke test that provisions an `AgentTeam`, lets the lead and a teammate exchange a single mailbox round-trip, and reports the effective StorageClass and AccessMode.

```bash
git clone https://github.com/amcheste/kagents.git
cd kagents
make mailbox-smoke-test
```

A passing run looks like:

```
PASS  StorageClass=nfs  AccessMode=ReadWriteMany  RoundTripMs=842
```

If `AccessMode` reports `ReadWriteOnce` or the test fails to schedule the second pod, your StorageClass isn't actually advertising RWX. Re-check step 3.

## Cost notes

EFS is billed by storage GB-month + provisioned throughput. For a typical kagents deployment running 5-10 teams concurrently:

- **Storage**: 1-5 GiB per team. At ~$0.30/GiB-month (Standard storage class), expect $0.50–$2/month for storage.
- **Throughput**: in `elastic` mode you pay per byte read/written (~$0.01/GiB). Idle teams cost nothing; active teams during a busy period might generate a few GiB of traffic per day.
- **Per-mount cost**: nothing. EFS mount targets are free.

The honest range for a small production install is **$5–$30/month**. For larger scale see the [EFS pricing page](https://aws.amazon.com/efs/pricing/).

## Common gotchas

??? warning "PVCs stuck in `Pending` with `failed to provision volume`"
    Almost always one of:
    - The EFS CSI driver pod isn't running. `kubectl get pods -n kube-system | grep efs`
    - The IAM role attached to the node group lacks `elasticfilesystem:CreateAccessPoint` and `DescribeAccessPoints`. The EKS add-on form attaches the right policy automatically; the upstream Helm install requires manual IAM setup. See [AWS docs](https://docs.aws.amazon.com/eks/latest/userguide/efs-csi.html#efs-create-iam-resources).
    - The StorageClass references a `fileSystemId` that doesn't exist or has no mount targets in the right subnets.

??? warning "Pods get stuck mounting with `mount.nfs4: Connection refused`"
    The worker security group doesn't allow inbound NFS from itself. Add a rule: source `<worker-sg-id>`, type `NFS`, port `2049`.

??? warning "Slow first-mount on a fresh PVC"
    EFS Access Point provisioning can take 30-60s on first use. After the first mount, subsequent mounts of the same PVC are fast. This is normal.

## Where to look next

- [Resource model](../../explanation/resources.md). The CRDs you'll be writing
- [Coordination protocol](../../explanation/coordination.md). Why RWX matters in detail
- [Operations](../../explanation/operations.md). Budget, RBAC, observability for the running operator
