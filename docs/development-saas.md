### Create a local VM to test provisioning

Install multipass:

```
sudo snap install multipass
```

Create an SSH key:

```
ssh-keygen -t ed25519 -C "maps-test-key"
```

Create a cloud-init.yaml:

```
ssh_authorized_keys:
  - "ssh-ed25519 AAAA... your_public_key ..."
```

Launch a VM with that config:

```
multipass launch -n dev --cloud-init cloud-init.yaml
```

For convenience I have already created an SSH key and the cloud-init.yaml

!!!!NEVER USE THEM on production!!!!

Steps

1. Create the VM
```
multipass launch -n mapsdev --cloud-init local/cloud-init-test.yaml
```

2. Get IP:

```
multipass info mapsdev
```

Get the ip

3. SSH:

```
ssh -i local/test_ssh_key ubuntu@<vm-ip>
```

