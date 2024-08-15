# Start a Quick Demo


## Bring up Virtual Machines
This project uses Vagrant tool for provisioning Virtual Machines automatically. The setup bash script contains the 
Linux instructions to install dependencies and plugins required for its usage. This script supports two 
Virtualization technologies (Libvirt and VirtualBox).

    $ sudo ./setup.sh -p libvirt
There is a default.yml in the ./config directory which creates multiple vm.

Once Vagrant is installed, it's possible to provision a vm using the following instructions:

    $ vagrant up
In-depth documentation and use cases of various Vagrant commands Vagrant commands is available on the Vagrant site.

## Deploy k8s cluster with FleetBoard

```bash
vagrant provision --provision-with deployment
```