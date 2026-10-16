# Bootstrapping lab credentials with CloudFormation

`lab-credentials.cfn.yaml` creates a dedicated IAM user + access key for running
the lab. Deploy it **once** in your isolated test account. This is the only
CloudFormation in the lab — the scenarios themselves are Terraform.

> ⚠️ Deploy only in a dedicated, throwaway test account. Delete the stack when
> you're done to revoke the key.

## Option A — AWS Console (no CLI needed)

1. Sign in to the **test account** console.
2. **CloudFormation → Create stack → With new resources (standard)**.
3. **Upload a template file** → choose
   `bellu-cloud-lab/scripts/lab-credentials.cfn.yaml`.
4. Stack name: `bellu-lab-credentials`.
5. Parameters:
   - `UserName`: `bellu-lab` (default is fine)
   - `AccessLevel`: `admin` (easiest for a disposable account) or `scoped`
     (only EC2/VPC/IAM/S3/STS).
6. On the review page, check **"I acknowledge that AWS CloudFormation might
   create IAM resources with custom names"**, then **Create stack**.
7. When status is `CREATE_COMPLETE`, open the **Outputs** tab and copy
   `AccessKeyId` and `SecretAccessKey`.

## Option B — AWS CLI

```bash
aws cloudformation deploy \
  --template-file bellu-cloud-lab/scripts/lab-credentials.cfn.yaml \
  --stack-name bellu-lab-credentials \
  --capabilities CAPABILITY_NAMED_IAM \
  --parameter-overrides UserName=bellu-lab AccessLevel=admin

# Read the key back out of the stack outputs:
aws cloudformation describe-stacks --stack-name bellu-lab-credentials \
  --query "Stacks[0].Outputs" --output table
```

## Put the key in the lab's own .env

```bash
cp bellu-cloud-lab/.env.example bellu-cloud-lab/.env
# then set:
#   AWS_ACCESS_KEY_ID=<AccessKeyId output>
#   AWS_SECRET_ACCESS_KEY=<SecretAccessKey output>
#   AWS_REGION=us-east-1
```

`bellu-cloud-lab/.env` is gitignored and separate from the main stack's
`deploy/.env`. The lab make targets load it automatically.

## Run a scenario

```bash
make lab-test SCENARIO=aws/public-ec2
```

## Clean up (revoke the key)

```bash
aws cloudformation delete-stack --stack-name bellu-lab-credentials
```

Deleting the stack removes the IAM user and its access key, so the credential
can no longer be used.
