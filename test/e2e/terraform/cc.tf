resource "random_password" "elastic_password" {
  length  = 30
  special = false
}

resource "tls_private_key" "ssh" {
  algorithm = "ED25519"
}

resource "digitalocean_ssh_key" "cc" {
  name       = lower("autossh-project-${var.project_name}")
  public_key = tls_private_key.ssh.public_key_openssh
}

resource "digitalocean_droplet" "cc" {
  # depends_on = [digitalocean_vpc.testnet-vpc]
  name     = "cc"
  image    = "debian-12-x64"
  region   = var.region
  tags     = concat(var.tags, ["cc", var.region])
  size     = var.cc_size
  ssh_keys = concat(var.ssh_keys, [digitalocean_ssh_key.cc.id])
  user_data = templatefile("user-data/cc-data.yaml", {
    grafana_data_sources      = filebase64("../monitoring/config-grafana/provisioning/datasources/prometheus.yml")
    grafana_dashboards_config = filebase64("../monitoring/config-grafana/provisioning/dashboards/default.yml")
    elastic_password          = random_password.elastic_password.result
  })
  connection {
    host        = digitalocean_droplet.cc.ipv4_address
    timeout     = var.ssh_timeout
    private_key = tls_private_key.ssh.private_key_openssh
  }
  provisioner "file" {
    source      = "../monitoring/config-grafana/provisioning/dashboards-data"
    destination = "/root"
  }
  provisioner "file" {
    content     = tls_private_key.ssh.private_key_openssh
    destination = "/root/.ssh/id_rsa"
  }
}

# Once cloud-init is done on CC, set up and start a DNS server.
resource "terraform_data" "cc-dns" {
  triggers_replace = [
    digitalocean_droplet.node,
    digitalocean_droplet.cc.id
  ]

  connection {
    host        = digitalocean_droplet.cc.ipv4_address
    timeout     = var.ssh_timeout
    private_key = tls_private_key.ssh.private_key_openssh
  }

  provisioner "file" {
    content     = templatefile("templates/hosts.tmpl", { nodes = local.nodes, cc = local.cc })
    destination = "/etc/hosts"
  }

  provisioner "remote-exec" {
    inline = [
      # "cloud-init status --wait  > /dev/null 2>&1",
      "while [ ! -f /etc/done ]; do sleep 1; done",
      "systemctl reload-or-restart dnsmasq"
    ]
  }
}

resource "terraform_data" "prometheus-config" {
  triggers_replace = [
    digitalocean_droplet.node[*]
  ]

  connection {
    host        = digitalocean_droplet.cc.ipv4_address
    timeout     = var.ssh_timeout
    private_key = tls_private_key.ssh.private_key_openssh
  }

  provisioner "file" {
    content     = templatefile("templates/prometheus-yml.tmpl", { nodes = local.nodes })
    destination = "/root/docker/prometheus.yml"
  }

  provisioner "remote-exec" {
    inline = [
      "while [ ! -f /root/docker/compose.yml ]; do sleep 1; done",
      "docker compose -f /root/docker/compose.yml --profile monitoring --progress quiet up -d"
    ]
  }
}
