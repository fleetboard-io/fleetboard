# How to build Nauti 

## Build the Nauti image

If you don't prefer to build `Nauti` images by yourself, you can directly pull images from the [ghcr.io/nauti-io](https://github.com/orgs/nauti-io/packages) registry.

### Prerequisites

- Docker / Podman

For contributor need to login to the `ghcr.io` registry.
Get a Github Token with `read:packages` and `write:packages` permissions.
On the dev env login into the `ghcr.io` registry with the following command:

```bash
# Login to the ghcr.io registry
# GHCR_USER is the Github username
# GHCR_PAT is the Github Personal Access Token
echo $GHCR_PAT | docker login ghcr.io -u $GHCR_USER --password-stdin
```

Clone the repo to local directory

```bash
git clonehttps://github.com/nauti-io/nauti.git
cd nauti
```

### Build the all Images and push to registry (ghcr.io)
```bash
make images
```

### DediNic Image

```bash
make dedinic-image
```

### Ep-Controller Image
    
```bash
make ep-controller-image
```


## Build the Nauti Binary

- todo