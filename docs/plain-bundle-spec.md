# Plain Bundle Spec

## Overview

This document is meant to define the plain bundle format as a reference for those publishing plain bundles for use with
RukPak. A bundle is a collection of Kubernetes resources that are packaged together for the purposes of installing onto
a Kubernetes cluster. A bundle can be unpacked onto a running cluster, where controllers can then create the underlying
content embedded in the bundle. The bundle can be used as the underlying `spec.source` for
a [Bundle](https://github.com/operator-framework/rukpak#bundle) resource.

A plain bundle is simply a collection of static, arbitrary, Kubernetes YAML manifests in a given directory. A plain
bundle can consist of a container image, a directory in a git repository, or any other content source that
the [plain bundle provisioner](https://github.com/operator-framework/rukpak/blob/main/internal/provisioner/plain/README.md)
supports.

The currently implemented plain bundle format is the `plain+v0` format. The name of the bundle format, `plain+v0`
combines the type of bundle (plain) with the current schema version (v0).
The [plain bundle provisioner](https://github.com/operator-framework/rukpak/blob/main/internal/provisioner/plain/README.md)
is able to source `plain+v0` bundles and install them onto a Kubernetes cluster.

> Note: the plain+v0 bundle format is at schema version v0, which means it's an experimental format that is subject
> to change.

Supported source types for a plain bundle currently include the following:

* A container image
* A directory in a git repository

Additional source types, such as a local volume or a generic URI-based resource, are on the roadmap. These source types
all present the same content, a directory containing static Kubernetes YAML manifests, in a different ways.

## Common Terminology

* `bundle` is a collection of files that define content to be deployed to a cluster
* `bundle image` is a container image that contains a bundle within its filesystem
* `bundle git repo` is a git repository that contains a bundle within a directory

## Example

For example, below is a minimal example of a Dockerfile that builds a `plain+v0` bundle image from a directory
containing static Kubernetes manifests.

```dockerfile
FROM scratch
COPY /manifests /manifests
```

where the given `manifests` directory contains the Kubernetes resources required to deploy an application, for example:

```tree
manifests
├── namespace.yaml
├── cluster_role.yaml
├── role.yaml
├── serviceaccount.yaml
├── cluster_role_binding.yaml
├── role_binding.yaml
└── deployment.yaml
```

For a bundle git repo, any directory that contains only static Kubernetes manifests checked into a git repository
accessible via a remote URL can be considered a plain bundle and sourced by the plain provisioner.

> Note: there must be at least one resource in the manifests directory in order for the bundle to be a valid
> plain+v0 bundle.

## Technical Details

* The static manifests must be located in the root-level /manifests directory in a bundle image for the bundle to be a
  valid `plain+v0` bundle that the provisioner can unpack. A plain bundle image without a /manifests directory is
  invalid and will not be successfully unpacked onto the cluster.
* For a bundle git repo, this limitation does not exist, and the manifests can be in any directory in the repository.
  The manifest directory is assumed to be ./manifests in a bundle git repo but can be provided at runtime.
* The manifests directory should be flat: all manifests should be at the top-level with no subdirectories.
* The plain bundle image can be built from any base image, but `scratch` is recommended as it keeps the resulting bundle
  image a minimal size.
* Including any content in the root `manifests` directory of a plain bundle that is not static manifests will result in
  a failure when creating content on-cluster from that bundle via
  a [BundleInstance](https://github.com/operator-framework/rukpak#bundleinstance). Essentially, any file that would not
  successfully `kubectl apply` will result in an error, but multi-object YAML files, or JSON files, are fine. There will
  be validation tooling provided that can determine whether a given artifact is a valid bundle.

## Quickstart

As an example, we can package the [combo operator](https://github.com/operator-framework/combo) into a `plain+v0` bundle
image by taking the following steps:

1. First let's pull down the combo repository.

```bash
git clone https://github.com/operator-framework/combo
```

2. Let's take a look at the combo manifests directory to make sure it is a valid bundle.

```bash
$ tree combo/manifests
combo/manifests
├── 00_combo.io_templates_crd.yaml
├── 01_combo.io_combinations_crd.yaml
├── 02_namespace.yaml
├── 03_service_account.yaml
├── 04_cluster_role.yaml
├── 05_cluster_role_binding.yaml
└── 06_deployment.yaml
```

This manifests directory is a flattened directory that contains arbitrary Kubernetes manifests and therefore can be
sourced and unpacked by the plain provisioner. Let's package it up as a plain bundle image.

3. Create a new Dockerfile at the root of the RukPak repository named Dockerfile.example

```bash
touch Dockerfile.example
```

4. Edit the Dockerfile to include the following:

```bash
cat <<EOF > Dockerfile.example
FROM scratch
COPY combo/manifests /manifests
EOF
```

5. Build the image using a container tool like docker or podman. Use an image tag that references a repository that you
   have push access to. For example,

```bash
docker build -f Dockerfile.example -t quay.io/operator-framework/plain-provisioner:example .
```

6. Push the image to the remote registry

```bash
docker push quay.io/operator-framework/plain-provisioner:example
```

7. Make sure rukpak is installed locally on a running cluster.

```bash
make run
```

8. Now that the plain bundle image has been built, it can be referenced in a Bundle and applied to the cluster.

```bash
kubectl apply -f -<<EOF
apiVersion: core.rukpak.io/v1alpha1
kind: Bundle
metadata:
  name: my-bundle
spec:
  source:
    type: image
    image:
      ref: quay.io/operator-framework/plain-provisioner:example
  provisionerClassName: core.rukpak.io/plain
EOF
```
