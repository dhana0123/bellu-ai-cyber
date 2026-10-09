package setup

import "fmt"

// renderManual produces step-by-step instructions for creating the role by
// hand. The trust and permission policies the customer must paste are the same
// authoritative documents the backend generated (returned on the Setup).
func renderManual(in Input) (*Artifact, error) {
	return &Artifact{
		Method: "manual",
		Instructions: []string{
			fmt.Sprintf("Step 1 — Create an IAM role named %s.", in.RoleName),
			"Step 2 — Set its trust policy to the provided trust_policy document (it conditions sts:AssumeRole on your external id).",
			"Step 3 — Attach the provided permission_policy document (least-privilege, read-only).",
			"Step 4 — Copy the role ARN and enter it in Bellu, then Verify Connection.",
		},
	}, nil
}
