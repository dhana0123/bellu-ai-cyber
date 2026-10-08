variable "name" { type = string }
variable "kms_key_arn" { type = string }
variable "raw_object_lock_days" {
  type    = number
  default = 3650
}
