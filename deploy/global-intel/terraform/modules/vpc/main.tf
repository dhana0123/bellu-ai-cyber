# A VPC with public + private subnets across az_count AZs. The pipeline task and
# data stores sit in private subnets; egress to upstream intel sources goes via
# NAT. single_nat_gateway toggles cost (one shared GW) vs HA (one per AZ).
data "aws_availability_zones" "available" {
  state = "available"
}

locals {
  azs          = slice(data.aws_availability_zones.available.names, 0, var.az_count)
  nat_gw_count = var.single_nat_gateway ? 1 : var.az_count
  # Carve /20 public + /20 private subnets deterministically from the VPC CIDR.
  public_cidrs  = [for i in range(var.az_count) : cidrsubnet(var.vpc_cidr, 4, i)]
  private_cidrs = [for i in range(var.az_count) : cidrsubnet(var.vpc_cidr, 4, i + 8)]
}

resource "aws_vpc" "this" {
  cidr_block           = var.vpc_cidr
  enable_dns_support   = true
  enable_dns_hostnames = true
  tags                 = { Name = "${var.name}-vpc" }
}

resource "aws_internet_gateway" "this" {
  vpc_id = aws_vpc.this.id
  tags   = { Name = "${var.name}-igw" }
}

resource "aws_subnet" "public" {
  count                   = var.az_count
  vpc_id                  = aws_vpc.this.id
  availability_zone       = local.azs[count.index]
  cidr_block              = local.public_cidrs[count.index]
  map_public_ip_on_launch = true
  tags                    = { Name = "${var.name}-public-${count.index + 1}", Tier = "public" }
}

resource "aws_subnet" "private" {
  count             = var.az_count
  vpc_id            = aws_vpc.this.id
  availability_zone = local.azs[count.index]
  cidr_block        = local.private_cidrs[count.index]
  tags              = { Name = "${var.name}-private-${count.index + 1}", Tier = "private" }
}

# Public routing.
resource "aws_route_table" "public" {
  vpc_id = aws_vpc.this.id
  tags   = { Name = "${var.name}-public-rt" }
}

resource "aws_route" "public_default" {
  route_table_id         = aws_route_table.public.id
  destination_cidr_block = "0.0.0.0/0"
  gateway_id             = aws_internet_gateway.this.id
}

resource "aws_route_table_association" "public" {
  count          = var.az_count
  subnet_id      = aws_subnet.public[count.index].id
  route_table_id = aws_route_table.public.id
}

# NAT gateways (count = 1 shared, or one per AZ).
resource "aws_eip" "nat" {
  count  = local.nat_gw_count
  domain = "vpc"
  tags   = { Name = "${var.name}-nat-eip-${count.index + 1}" }
}

resource "aws_nat_gateway" "this" {
  count         = local.nat_gw_count
  allocation_id = aws_eip.nat[count.index].id
  subnet_id     = aws_subnet.public[count.index].id
  tags          = { Name = "${var.name}-nat-${count.index + 1}" }
  depends_on    = [aws_internet_gateway.this]
}

# Private routing: each private subnet routes to its AZ's NAT GW, or the single
# shared one when single_nat_gateway is set.
resource "aws_route_table" "private" {
  count  = var.az_count
  vpc_id = aws_vpc.this.id
  tags   = { Name = "${var.name}-private-rt-${count.index + 1}" }
}

resource "aws_route" "private_default" {
  count                  = var.az_count
  route_table_id         = aws_route_table.private[count.index].id
  destination_cidr_block = "0.0.0.0/0"
  nat_gateway_id         = aws_nat_gateway.this[var.single_nat_gateway ? 0 : count.index].id
}

resource "aws_route_table_association" "private" {
  count          = var.az_count
  subnet_id      = aws_subnet.private[count.index].id
  route_table_id = aws_route_table.private[count.index].id
}

# Gateway VPC endpoint for S3 so raw/release traffic avoids NAT.
resource "aws_vpc_endpoint" "s3" {
  vpc_id            = aws_vpc.this.id
  service_name      = "com.amazonaws.${var.region}.s3"
  vpc_endpoint_type = "Gateway"
  route_table_ids   = aws_route_table.private[*].id
  tags              = { Name = "${var.name}-s3-endpoint" }
}

# ---------------------------------------------------------------------------
# Security groups live here (not in ecs) so compute and the data stores both
# depend on the network layer, never on each other — keeping the module graph
# acyclic. task = egress-only; db/search accept ingress only from the task SG.
# ---------------------------------------------------------------------------
resource "aws_security_group" "task" {
  name_prefix = "${var.name}-task-"
  description = "${var.name} Fargate task (egress only)"
  vpc_id      = aws_vpc.this.id
  egress {
    description = "Egress to upstream intel sources + AWS APIs"
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
  tags = { Name = "${var.name}-task-sg" }
  lifecycle { create_before_destroy = true }
}

resource "aws_security_group" "database" {
  name_prefix = "${var.name}-db-"
  description = "${var.name} RDS PostgreSQL"
  vpc_id      = aws_vpc.this.id
  ingress {
    description     = "PostgreSQL from the pipeline task only"
    from_port       = 5432
    to_port         = 5432
    protocol        = "tcp"
    security_groups = [aws_security_group.task.id]
  }
  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
  tags = { Name = "${var.name}-db-sg" }
  lifecycle { create_before_destroy = true }
}

resource "aws_security_group" "search" {
  name_prefix = "${var.name}-search-"
  description = "${var.name} OpenSearch"
  vpc_id      = aws_vpc.this.id
  ingress {
    description     = "HTTPS to OpenSearch from the pipeline task only"
    from_port       = 443
    to_port         = 443
    protocol        = "tcp"
    security_groups = [aws_security_group.task.id]
  }
  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
  tags = { Name = "${var.name}-search-sg" }
  lifecycle { create_before_destroy = true }
}
