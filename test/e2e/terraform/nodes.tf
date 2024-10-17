resource "digitalocean_droplet" "node" {
  # depends_on = [digitalocean_vpc.testnet-vpc]
  count      = local.testnet_size
  name       = var.node_names[count.index]
  image      = "debian-12-x64"
  region     = var.region
  tags       = concat(var.tags, [var.region])
  size       = var.small
  vpc_uuid   = digitalocean_vpc.testnet-vpc.id
  ssh_keys   = concat(var.ssh_keys, [digitalocean_ssh_key.cc.id])
  user_data = templatefile("user-data/nodes-data.yaml", {
    id = var.node_names[count.index]
    cc = {
      name        = digitalocean_droplet.cc.name
      ip          = digitalocean_droplet.cc.ipv4_address
      internal_ip = digitalocean_droplet.cc.ipv4_address_private
    }
    elastic_password = random_password.elastic_password.result
  })
}

# Create infra file with nodes info as required by runner.
resource "local_file" "infra_data" {
  depends_on = [
    digitalocean_droplet.node,
  ]
  content = templatefile("${path.module}/templates/infra-data-json.tmpl", {
    subnet = var.vpc_subnet,
    nodes  = local.nodes
  })
  filename = "../${var.testnet_dir}/infra-data.json"
}

# Run setup when infra_data file is ready.
# resource "terraform_data" "setup" {
#   depends_on = [
#     local_file.infra_data,
#   ]

#   provisioner "local-exec" {
#     command     = "./build/runner -f ${var.manifest_path} -t DO setup"
#     working_dir = ".."
#   }
# }

# After a node is created and cloud-init is done, mount the NFS directory.
resource "terraform_data" "node-done" {
  triggers_replace = [
    digitalocean_droplet.node[count.index],
    terraform_data.cc-nfs.id
  ]

  count = local.testnet_size

  connection {
    host        = digitalocean_droplet.node[count.index].ipv4_address
    timeout     = "120s"
    private_key = tls_private_key.ssh.private_key_openssh
  }

  provisioner "remote-exec" {
    inline = [
      "cloud-init status --wait > /dev/null 2>&1",
      # mount NFS directory
      "mount /data"
    ]
  }
}
