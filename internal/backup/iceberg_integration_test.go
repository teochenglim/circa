package backup

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestIcebergWriter_AppendAndReadBack is a real integration test against a
// live Iceberg REST catalog + S3-compatible warehouse — it does not run by
// default (no such infrastructure exists in a normal `go test ./...`
// environment). Set CIRCA_TEST_ICEBERG_URI (and friends below) to a real
// REST catalog to opt in — verified during v0.7.0 development against
// apache/iceberg-rest-fixture + minio/minio run locally via Docker:
//
//	docker network create iceberg-test
//	docker run -d --name minio --network iceberg-test -p 9002:9000 -p 9003:9001 \
//	  -e MINIO_ROOT_USER=admin -e MINIO_ROOT_PASSWORD=password123 \
//	  minio/minio server /data --console-address ":9001"
//	docker exec minio mc alias set local http://localhost:9000 admin password123
//	docker exec minio mc mb local/warehouse
//	docker run -d --name iceberg-rest --network iceberg-test -p 8181:8181 \
//	  -e CATALOG_WAREHOUSE=s3://warehouse/ \
//	  -e CATALOG_IO__IMPL=org.apache.iceberg.aws.s3.S3FileIO \
//	  -e CATALOG_S3_ENDPOINT=http://minio:9000 \
//	  -e CATALOG_S3_ACCESS__KEY__ID=admin \
//	  -e CATALOG_S3_SECRET__ACCESS__KEY=password123 \
//	  -e CATALOG_S3_PATH__STYLE__ACCESS=true \
//	  -e AWS_REGION=us-east-1 \
//	  apache/iceberg-rest-fixture:latest
//	AWS_ACCESS_KEY_ID=admin AWS_SECRET_ACCESS_KEY=password123 \
//	CIRCA_TEST_ICEBERG_URI=http://localhost:8181 \
//	CIRCA_TEST_ICEBERG_WAREHOUSE=s3://warehouse/ \
//	CIRCA_TEST_ICEBERG_S3_ENDPOINT=http://localhost:9002 \
//	  go test ./internal/backup/... -run TestIcebergWriter -v
func TestIcebergWriter_AppendAndReadBack(t *testing.T) {
	uri := os.Getenv("CIRCA_TEST_ICEBERG_URI")
	if uri == "" {
		t.Skip("CIRCA_TEST_ICEBERG_URI not set - skipping live Iceberg integration test (see this test's doc comment to run it)")
	}
	warehouse := os.Getenv("CIRCA_TEST_ICEBERG_WAREHOUSE")
	s3Endpoint := os.Getenv("CIRCA_TEST_ICEBERG_S3_ENDPOINT")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	writer, err := NewIcebergWriter(ctx, uri, warehouse, s3Endpoint, "us-east-1", "")
	if err != nil {
		t.Fatalf("NewIcebergWriter: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	rows := []Row{
		{
			NodeID: "test-node", Hostname: "test-host",
			MetricName: "node_cpu_seconds_total",
			Labels:     map[string]string{"cpu": "0", "mode": "idle"},
			Time:       now, Value: 42.5, Anomalous: false,
		},
	}

	if err := writer.Append(ctx, rows); err != nil {
		t.Fatalf("Append: %v", err)
	}

	// A second append with different rows confirms incremental (not
	// overwriting) snapshots — the core watermark-driven CDC assumption.
	rows2 := []Row{
		{
			NodeID: "test-node", Hostname: "test-host",
			MetricName: "node_load1",
			Time:       now.Add(time.Minute), Value: 1.5, Anomalous: false,
		},
	}
	if err := writer.Append(ctx, rows2); err != nil {
		t.Fatalf("second Append: %v", err)
	}

	tbl, err := writer.ensureTable(ctx)
	if err != nil {
		t.Fatalf("ensureTable (read-back): %v", err)
	}
	arrTbl, err := tbl.Scan().ToArrowTable(ctx)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	defer arrTbl.Release()

	if arrTbl.NumRows() < 2 {
		t.Errorf("got %d rows after two appends, want at least 2", arrTbl.NumRows())
	}
}

// TestIcebergWriter_AppendEmptyIsNoop confirms Append tolerates an empty
// slice without making any catalog calls at all — still runs without live
// infrastructure since NewIcebergWriter itself needs a reachable catalog
// to construct, so this is gated the same way.
func TestIcebergWriter_AppendEmptyIsNoop(t *testing.T) {
	uri := os.Getenv("CIRCA_TEST_ICEBERG_URI")
	if uri == "" {
		t.Skip("CIRCA_TEST_ICEBERG_URI not set - skipping live Iceberg integration test")
	}
	warehouse := os.Getenv("CIRCA_TEST_ICEBERG_WAREHOUSE")
	s3Endpoint := os.Getenv("CIRCA_TEST_ICEBERG_S3_ENDPOINT")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	writer, err := NewIcebergWriter(ctx, uri, warehouse, s3Endpoint, "us-east-1", "")
	if err != nil {
		t.Fatalf("NewIcebergWriter: %v", err)
	}
	if err := writer.Append(ctx, nil); err != nil {
		t.Errorf("Append(nil) = %v, want nil (no-op)", err)
	}
}
