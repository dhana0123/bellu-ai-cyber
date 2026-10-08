//go:build integration

package connector

import (
	"context"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/bellu/bellu-ai-cyber/internal/connector/provider"
	"github.com/bellu/bellu-ai-cyber/internal/store"
)

// newRepo spins up a disposable migrated Postgres and returns a Repository plus
// a seeded (orgID, projectID).
func newRepo(t *testing.T) (*Repository, int64, int64) {
	t.Helper()
	ctx := context.Background()
	ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("bellu"),
		tcpostgres.WithUsername("bellu"),
		tcpostgres.WithPassword("bellu"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second)),
	)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() { _ = ctr.Terminate(ctx) })

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("conn string: %v", err)
	}
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(st.Close)
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var orgID, projID int64
	if err := st.Pool().QueryRow(ctx,
		`INSERT INTO organizations (name) VALUES ('acme') RETURNING id`).Scan(&orgID); err != nil {
		t.Fatalf("seed org: %v", err)
	}
	if err := st.Pool().QueryRow(ctx,
		`INSERT INTO projects (org_id, key, name) VALUES ($1,'prod','Prod') RETURNING id`, orgID).Scan(&projID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	return NewRepository(st), orgID, projID
}

func sampleConn(orgID, projID int64) *Connection {
	return &Connection{
		OrgID:        orgID,
		ProjectID:    projID,
		Provider:     "aws",
		AccountID:    "123456789012",
		RoleARN:      "arn:aws:iam::123456789012:role/BelluConnectorRole",
		ExternalID:   "BELLU-TEST",
		Region:       "us-east-1",
		Capabilities: []string{"core"},
		Status:       StatusPending,
		CreatedAt:    time.Now().UTC(),
	}
}

func TestRepositoryRoundTrip(t *testing.T) {
	ctx := context.Background()
	repo, orgID, projID := newRepo(t)

	id, err := repo.Insert(ctx, sampleConn(orgID, projID))
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	got, err := repo.Get(ctx, orgID, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.AccountID != "123456789012" || got.Status != StatusPending || len(got.Capabilities) != 1 {
		t.Errorf("unexpected connection %+v", got)
	}
	if got.LastVerifiedAt != nil {
		t.Error("last_verified_at should be nil before verification")
	}

	list, err := repo.ListByOrg(ctx, orgID)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: err=%v len=%d", err, len(list))
	}

	if err := repo.UpdateStatus(ctx, orgID, id, StatusConnected); err != nil {
		t.Fatalf("update status: %v", err)
	}
	ts := time.Now().UTC()
	if err := repo.SetVerified(ctx, orgID, id, ts); err != nil {
		t.Fatalf("set verified: %v", err)
	}
	got, _ = repo.Get(ctx, orgID, id)
	if got.Status != StatusConnected || got.LastVerifiedAt == nil {
		t.Errorf("after update: status=%s verified=%v", got.Status, got.LastVerifiedAt)
	}

	if err := repo.Delete(ctx, orgID, id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := repo.Get(ctx, orgID, id); err != ErrNotFound {
		t.Errorf("after delete want ErrNotFound, got %v", err)
	}
}

// TestRepositoryTenantScoping asserts a connection created under org A is never
// returned to a query scoped to org B.
func TestRepositoryTenantScoping(t *testing.T) {
	ctx := context.Background()
	repo, orgA, projA := newRepo(t)

	// Seed a second org+project via a raw insert using the same pool.
	// The repo doesn't expose the pool, so create org B through a second project
	// under a distinct organization by reusing the connection's store pool.
	var orgB, projB int64
	if err := repo.pool.QueryRow(ctx,
		`INSERT INTO organizations (name) VALUES ('globex') RETURNING id`).Scan(&orgB); err != nil {
		t.Fatalf("seed org B: %v", err)
	}
	if err := repo.pool.QueryRow(ctx,
		`INSERT INTO projects (org_id, key, name) VALUES ($1,'prod','Prod') RETURNING id`, orgB).Scan(&projB); err != nil {
		t.Fatalf("seed project B: %v", err)
	}

	idA, err := repo.Insert(ctx, sampleConn(orgA, projA))
	if err != nil {
		t.Fatalf("insert A: %v", err)
	}

	// Org B cannot Get org A's connection.
	if _, err := repo.Get(ctx, orgB, idA); err != ErrNotFound {
		t.Errorf("org B Get of org A connection: want ErrNotFound, got %v", err)
	}
	// Org B's list is empty.
	listB, err := repo.ListByOrg(ctx, orgB)
	if err != nil {
		t.Fatalf("list B: %v", err)
	}
	if len(listB) != 0 {
		t.Errorf("org B list = %d, want 0", len(listB))
	}
	// Org B cannot update/delete org A's connection.
	if err := repo.UpdateStatus(ctx, orgB, idA, StatusError); err != ErrNotFound {
		t.Errorf("org B update of org A: want ErrNotFound, got %v", err)
	}
}

// TestSaveVerification asserts verification results persist with PASS/FAIL/
// NOT_ENABLED outcomes.
func TestSaveVerification(t *testing.T) {
	ctx := context.Background()
	repo, orgID, projID := newRepo(t)
	id, err := repo.Insert(ctx, sampleConn(orgID, projID))
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	res := &provider.VerifyResult{
		OK: false,
		Capabilities: []provider.CapabilityResult{
			{Capability: "core", Outcome: provider.OutcomeFail, Checks: []provider.PermissionResult{
				{Capability: "core", Permission: "iam:ListRoles", Outcome: provider.OutcomePass},
				{Capability: "core", Permission: "s3:ListAllMyBuckets", Outcome: provider.OutcomeFail},
			}},
			{Capability: "eks", Outcome: provider.OutcomeNotEnabled},
		},
	}
	if err := repo.SaveVerification(ctx, id, res); err != nil {
		t.Fatalf("save verification: %v", err)
	}

	var count int
	if err := repo.pool.QueryRow(ctx,
		`SELECT count(*) FROM connector_verification_results WHERE connection_id = $1`, id).Scan(&count); err != nil {
		t.Fatalf("count results: %v", err)
	}
	// 2 core permission rows + 1 eks NOT_ENABLED aggregate row.
	if count != 3 {
		t.Errorf("verification rows = %d, want 3", count)
	}
}

// TestRecordAndLatestSession asserts session upsert and latest lookup.
func TestRecordAndLatestSession(t *testing.T) {
	ctx := context.Background()
	repo, orgID, projID := newRepo(t)
	id, err := repo.Insert(ctx, sampleConn(orgID, projID))
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	start := time.Now().UTC()
	if err := repo.RecordSession(ctx, id, "disc-1", "run-1", "running", start, nil, ""); err != nil {
		t.Fatalf("record session: %v", err)
	}
	end := start.Add(time.Minute)
	if err := repo.RecordSession(ctx, id, "disc-1", "run-1", "complete", start, &end, ""); err != nil {
		t.Fatalf("update session: %v", err)
	}

	latest, err := repo.LatestSession(ctx, id)
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	if latest == nil || latest.Status != "complete" || latest.EndedAt == nil {
		t.Errorf("latest session = %+v, want complete with ended_at", latest)
	}
}
