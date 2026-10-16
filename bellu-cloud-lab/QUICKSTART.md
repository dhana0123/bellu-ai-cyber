# Cloud Lab — dev quickstart

Spin a scenario up, look at it, tear it down. For day-to-day development.

TL;DR:

```bash
make lab-up    SCENARIO=aws/public-ec2   # create + LEAVE running
make lab-down  SCENARIO=aws/public-ec2   # destroy when done
```

---

## The two ways to run

| Command | What it does | Use when |
| --- | --- | --- |
| `make lab-up SCENARIO=...` | Creates resources and **stops**. They stay until you destroy them. | You want to **see** resources / keep them around while developing. |
| `make lab-test SCENARIO=...` | Create → run Bellu → check graph → **auto-destroy**. Nothing is left behind. | You want to **validate** Bellu detects them (a test, not a deployment). |
| `make lab-down SCENARIO=...` | Destroys whatever `lab-up` left running. | Cleanup after `lab-up`. |

> `lab-up` is the one for development. `lab-test` deletes everything at the end
> on purpose, so you can't inspect it afterwards.

Scenarios available today: `aws/public-ec2`, `aws/iam-to-s3`.

---

## One-time setup

1. **Credentials** — deploy the CFN stack once in your test account, then put
   the key in the lab's own env file (separate from the main stack):

   ```bash
   # in your test account (admin is easiest for a disposable account):
   aws cloudformation deploy \
     --template-file bellu-cloud-lab/scripts/lab-credentials.cfn.yaml \
     --stack-name bellu-lab-credentials \
     --capabilities CAPABILITY_NAMED_IAM \
     --parameter-overrides AccessLevel=admin

   cp bellu-cloud-lab/.env.example bellu-cloud-lab/.env
   # set AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY / AWS_REGION from the stack Outputs
   ```

2. **Tools** — `terraform` and (for `lab-test`) a running Bellu collector + API:

   ```bash
   ./bellu-cloud-lab/scripts/steampipe-up.sh   # brings up steampipe + api + datastores
   ```

The lab make targets auto-load `bellu-cloud-lab/.env`, so you don't export
anything by hand.

---

## Spin up → inspect → tear down

```bash
# 1. create and leave running
make lab-up SCENARIO=aws/public-ec2

# 2. look at it (either option)
#    a) AWS console: Resource Groups → Tag Editor → tag bellu-lab = true
#    b) from the CLI (loads the lab .env for you):
set -a && . ./bellu-cloud-lab/.env && set +a
aws ec2 describe-instances --filters Name=tag:bellu-lab,Values=true \
  --query "Reservations[].Instances[].{id:InstanceId,state:State.Name}" --output table
aws s3 ls | grep bellu-lab
aws iam list-roles --query "Roles[?starts_with(RoleName,'public-ec2')].RoleName" --output table

# 3. destroy it
make lab-down SCENARIO=aws/public-ec2
```

Everything the lab creates is tagged `bellu-lab = true` and named `<scenario>*`
(e.g. `public-ec2`, `bellu-lab-public-ec2-<suffix>`), in region `AWS_REGION`.

---

## Validate Bellu against a scenario (the test cycle)

```bash
make lab-test SCENARIO=aws/public-ec2
```

Runs apply → `bellu run` → compares the graph to `scenario.yaml` →
**destroys everything** → prints PASS/FAIL with any missing nodes/edges.
Takes a few minutes (EC2 boot + Steampipe collection).

---

## Cleanup / safety

- **Forgot what's running?** List it:
  ```bash
  set -a && . ./bellu-cloud-lab/.env && set +a
  aws resourcegroupstaggingapi get-resources --tag-filters Key=bellu-lab,Values=true \
    --query "ResourceTagMappingList[].ResourceARN" --output table
  ```
- **Nuke everything the lab provisioned (all scenarios):**
  ```bash
  make destroy-all
  ```
- **Revoke the lab credentials when you're done developing:**
  ```bash
  aws cloudformation delete-stack --stack-name bellu-lab-credentials
  ```

---

## Gotchas

- `lab-test` leaves **nothing** behind by design — use `lab-up` to inspect.
- The scenario opens a security group to `0.0.0.0/0` — only ever run in an
  **isolated test account**.
- If `apply` fails with an IAM `AccessDenied`, the credentials policy is missing
  an action — redeploy the CFN stack (or use `AccessLevel=admin`). The run still
  auto-destroys whatever it created, so nothing is left behind.
- Resources cost money while up (one `t3.micro` + no NAT ≈ pennies/hour) — run
  `lab-down` when you stop developing for the day.
