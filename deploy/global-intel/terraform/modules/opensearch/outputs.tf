output "endpoint" { value = "https://${aws_opensearch_domain.this.endpoint}" }
output "domain_arn" { value = aws_opensearch_domain.this.arn }
output "domain_name" { value = aws_opensearch_domain.this.domain_name }
