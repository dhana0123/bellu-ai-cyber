// Package k8swi parses Kubernetes service-account annotations that bridge a
// cluster workload identity to a cloud identity.
//
// Supported bridges:
//
//	IRSA (EKS):   eks.amazonaws.com/role-arn      -> AWS IAM role ARN
//	GKE WI:       iam.gke.io/gcp-service-account   -> GCP service account email
//	Azure WI:     azure.workload.identity/client-id -> Azure MI client id
package k8swi

// Provider identifies the cloud a workload-identity binding targets.
type Provider string

const (
	ProviderAWS   Provider = "aws"
	ProviderGCP   Provider = "gcp"
	ProviderAzure Provider = "azure"
)

// Annotation keys for each workload-identity scheme.
const (
	AnnIRSA    = "eks.amazonaws.com/role-arn"
	AnnGKEWI   = "iam.gke.io/gcp-service-account"
	AnnAzureWI = "azure.workload.identity/client-id"
)

// Binding is a parsed workload-identity link: the cloud identity key a service
// account authenticates as.
type Binding struct {
	Provider Provider
	// CloudIdentityKey is the native identity key the SA maps to:
	//   AWS:   role ARN
	//   GCP:   service account email
	//   Azure: managed identity client id
	CloudIdentityKey string
	// AnnotationKey records which annotation produced the binding.
	AnnotationKey string
}

// Parse inspects a service account's annotations and returns any
// workload-identity bindings found (zero or more).
func Parse(annotations map[string]string) []Binding {
	var out []Binding
	if v := annotations[AnnIRSA]; v != "" {
		out = append(out, Binding{Provider: ProviderAWS, CloudIdentityKey: v, AnnotationKey: AnnIRSA})
	}
	if v := annotations[AnnGKEWI]; v != "" {
		out = append(out, Binding{Provider: ProviderGCP, CloudIdentityKey: v, AnnotationKey: AnnGKEWI})
	}
	if v := annotations[AnnAzureWI]; v != "" {
		out = append(out, Binding{Provider: ProviderAzure, CloudIdentityKey: v, AnnotationKey: AnnAzureWI})
	}
	return out
}
