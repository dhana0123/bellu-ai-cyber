package aws

import "strings"

// ActionCatalog is pure data describing how to evaluate IAM access for one AWS
// resource kind. It carries NO evaluator logic — the generic Evaluate function
// in policy.go consumes it. Adding support for a new resource kind is therefore
// a data-only change: add one entry to Catalogs (R9.2).
type ActionCatalog struct {
	// Service is the ARN service segment / IAM action prefix ("s3", "dynamodb",
	// "rds", "efs", "elasticache", ...).
	Service string
	// ResourceForms expands a target resource ARN into every ARN form a policy
	// statement might name for it (e.g. an S3 bucket plus its object space
	// bucket+"/*"). The returned slice is matched against statement Resources.
	ResourceForms func(arn string) []string
	// Representative is the catalog of concrete actions used to expand wildcard
	// actions (e.g. "s3:*", "s3:Get*") into trackable concrete actions.
	Representative []string
	// Classify optionally maps a concrete (lower-cased) action to an explicit
	// access level. Marker-based classification is sufficient for most kinds;
	// this is consulted first when present.
	Classify map[string]Access
	// AdminMarkers are substrings that force an admin classification when found
	// in an allowed action (lower-cased contains match). The service wildcard
	// (e.g. "s3:*") is always treated as admin.
	AdminMarkers []string
	// WriteMarkers are substrings that force a write classification.
	WriteMarkers []string
}

// serviceWildcard is the "<service>:*" action for a catalog.
func (c ActionCatalog) serviceWildcard() string { return c.Service + ":*" }

// matchesAction reports whether an IAM action string belongs to this catalog's
// service ("*" matches every service; "<service>:..." matches by prefix).
func (c ActionCatalog) matchesAction(act string) bool {
	la := strings.ToLower(act)
	return la == "*" || strings.HasPrefix(la, c.Service+":")
}

// expandAction expands a wildcard action into representative concrete actions,
// or returns the action itself when it is concrete. Mirrors the historical
// expandS3Action behavior but driven by the catalog's Representative list.
func (c ActionCatalog) expandAction(act string) []string {
	if !strings.Contains(act, "*") {
		return []string{act}
	}
	if act == c.serviceWildcard() || act == "*" {
		return append(append([]string{}, c.Representative...), c.serviceWildcard())
	}
	prefix := strings.TrimSuffix(act, "*")
	var out []string
	for _, concrete := range c.Representative {
		if strings.HasPrefix(strings.ToLower(concrete), strings.ToLower(prefix)) {
			out = append(out, concrete)
		}
	}
	if len(out) == 0 {
		out = []string{act}
	}
	return out
}

// classify maps a set of allowed concrete actions to an access level using the
// catalog's Classify map (if any) and its admin/write markers, with
// admin > write > read precedence.
func (c ActionCatalog) classify(allowed map[string]bool) Access {
	// Explicit per-action classification first (admin wins).
	best := AccessNone
	for a := range allowed {
		if lvl, ok := c.Classify[strings.ToLower(a)]; ok {
			best = maxAccess(best, lvl)
		}
	}
	// Admin markers / service wildcard.
	for a := range allowed {
		la := strings.ToLower(a)
		if la == c.serviceWildcard() {
			return AccessAdmin
		}
		for _, m := range c.AdminMarkers {
			if strings.Contains(la, m) {
				return AccessAdmin
			}
		}
	}
	if best == AccessAdmin {
		return AccessAdmin
	}
	// Write markers.
	for a := range allowed {
		la := strings.ToLower(a)
		for _, m := range c.WriteMarkers {
			if strings.Contains(la, m) {
				return AccessWrite
			}
		}
	}
	if best != AccessNone {
		return best
	}
	return AccessRead
}

// maxAccess returns the higher of two access levels (admin > write > read > none).
func maxAccess(a, b Access) Access {
	rank := map[Access]int{AccessNone: 0, AccessRead: 1, AccessWrite: 2, AccessAdmin: 3}
	if rank[b] > rank[a] {
		return b
	}
	return a
}

// Catalogs is the per-resource-kind action-catalog registry keyed by service.
// Adding a new resource kind = adding one entry here, with zero changes to the
// generic Evaluate logic (R9.2).
var Catalogs = map[string]ActionCatalog{
	// S3 reproduces today's behavior EXACTLY: ResourceForms -> {bucket,
	// bucket+"/*"}; Representative is the historical expandS3Action catalog;
	// admin/write markers match the former classify/isWriteAction.
	"s3": {
		Service: "s3",
		ResourceForms: func(arn string) []string {
			return []string{arn, arn + "/*"}
		},
		Representative: []string{
			"s3:GetObject", "s3:GetObjectVersion", "s3:ListBucket", "s3:GetBucketLocation",
			"s3:PutObject", "s3:DeleteObject", "s3:RestoreObject",
			"s3:PutBucketPolicy", "s3:PutBucketAcl", "s3:DeleteBucket",
		},
		AdminMarkers: []string{"putbucketpolicy", "putbucketacl", "deletebucket", "putaccountpublicaccessblock"},
		WriteMarkers: []string{"put", "delete", "create", "write", "restore", "replicate"},
	},

	// DynamoDB: Get/Query/Scan=read, Put/Update/Delete/BatchWrite=write,
	// DeleteTable/PutResourcePolicy=admin. Markers are approximate.
	"dynamodb": {
		Service: "dynamodb",
		ResourceForms: func(arn string) []string {
			return []string{arn, arn + "/*"}
		},
		Representative: []string{
			"dynamodb:GetItem", "dynamodb:BatchGetItem", "dynamodb:Query", "dynamodb:Scan",
			"dynamodb:PutItem", "dynamodb:UpdateItem", "dynamodb:DeleteItem", "dynamodb:BatchWriteItem",
			"dynamodb:DeleteTable", "dynamodb:PutResourcePolicy",
		},
		AdminMarkers: []string{"deletetable", "putresourcepolicy", "deletebackup", "createtable"},
		WriteMarkers: []string{"putitem", "updateitem", "deleteitem", "batchwrite", "put", "update", "delete", "write"},
	},

	// RDS: Describe*=read, Modify*/Reboot*=write, Delete*/elevated=admin.
	"rds": {
		Service: "rds",
		ResourceForms: func(arn string) []string {
			return []string{arn, arn + ":*"}
		},
		Representative: []string{
			"rds:DescribeDBInstances", "rds:DescribeDBClusters",
			"rds:ModifyDBInstance", "rds:RebootDBInstance",
			"rds:DeleteDBInstance", "rds:DeleteDBCluster",
		},
		AdminMarkers: []string{"delete", "modifydbclusterendpoint", "restoredbcluster", "failover"},
		WriteMarkers: []string{"modify", "reboot", "create", "stop", "start", "restore"},
	},

	// EFS: DescribeFileSystems/ClientMount=read, ClientWrite=write,
	// DeleteFileSystem/PutFileSystemPolicy=admin.
	"efs": {
		Service: "elasticfilesystem",
		ResourceForms: func(arn string) []string {
			return []string{arn, arn + "/*"}
		},
		Representative: []string{
			"elasticfilesystem:DescribeFileSystems", "elasticfilesystem:ClientMount",
			"elasticfilesystem:ClientWrite",
			"elasticfilesystem:DeleteFileSystem", "elasticfilesystem:PutFileSystemPolicy",
		},
		AdminMarkers: []string{"delete", "putfilesystempolicy", "putbackuppolicy"},
		WriteMarkers: []string{"clientwrite", "create", "modify", "put", "write"},
	},

	// ElastiCache: Describe*=read, Modify*=write, Delete*=admin.
	"elasticache": {
		Service: "elasticache",
		ResourceForms: func(arn string) []string {
			return []string{arn, arn + ":*"}
		},
		Representative: []string{
			"elasticache:DescribeCacheClusters", "elasticache:DescribeReplicationGroups",
			"elasticache:ModifyCacheCluster",
			"elasticache:DeleteCacheCluster",
		},
		AdminMarkers: []string{"delete", "modifycacheclusterpolicy", "revoke"},
		WriteMarkers: []string{"modify", "create", "reboot", "increase", "decrease"},
	},
}
