package backup

import (
	"context"
	"fmt"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/memory"
	"github.com/apache/iceberg-go"
	"github.com/apache/iceberg-go/catalog"
	_ "github.com/apache/iceberg-go/catalog/rest" // registers the "rest" catalog type
	_ "github.com/apache/iceberg-go/io/gocloud"   // registers s3://, gs://, azblob:// IO
	"github.com/apache/iceberg-go/table"
)

// icebergSchema is DESIGN/07 §7.4's table schema — see Row's doc comment
// for the two departures (no env/region, labels as a JSON string not a
// native map column).
var icebergSchema = iceberg.NewSchema(0,
	iceberg.NestedField{ID: 1, Name: "node_id", Type: iceberg.PrimitiveTypes.String, Required: true},
	iceberg.NestedField{ID: 2, Name: "hostname", Type: iceberg.PrimitiveTypes.String, Required: true},
	iceberg.NestedField{ID: 3, Name: "metric_name", Type: iceberg.PrimitiveTypes.String, Required: true},
	iceberg.NestedField{ID: 4, Name: "labels_json", Type: iceberg.PrimitiveTypes.String, Required: true},
	iceberg.NestedField{ID: 5, Name: "ts", Type: iceberg.PrimitiveTypes.TimestampTz, Required: true},
	iceberg.NestedField{ID: 6, Name: "value", Type: iceberg.PrimitiveTypes.Float64, Required: true},
	iceberg.NestedField{ID: 7, Name: "anomaly_bit", Type: iceberg.PrimitiveTypes.Bool, Required: true},
)

const (
	tableNamespace = "circa"
	tableName      = "metrics"
)

// IcebergWriter appends Rows to the configured Iceberg table as one new
// snapshot per call, partitioned by (day(ts), node_id) per DESIGN/07 §7.4
// so both time-range pruning and per-node federation queries (§7.5) stay
// cheap.
type IcebergWriter struct {
	cat catalog.Catalog
}

// NewIcebergWriter loads (does not create) the catalog connection.
// Credentials for the S3-compatible warehouse come from the standard
// AWS_ACCESS_KEY_ID/AWS_SECRET_ACCESS_KEY(/AWS_SESSION_TOKEN) env vars —
// the AWS SDK's own default credential chain — not config.yaml, per
// DESIGN/07 §7.6's "keep credentials centralized, config-light." An
// optional CIRCA_BACKUP_CATALOG_TOKEN env var is sent as a REST catalog
// bearer token, for catalogs that gate access separately from S3 itself.
func NewIcebergWriter(ctx context.Context, catalogURI, warehouse, s3Endpoint, s3Region, catalogToken string) (*IcebergWriter, error) {
	props := iceberg.Properties{
		"uri":       catalogURI,
		"warehouse": warehouse,
	}
	if s3Endpoint != "" {
		props["s3.endpoint"] = s3Endpoint
		props["s3.path-style-access"] = "true" // path-style is the common case for self-hosted S3-compatible endpoints (MinIO, etc.)
	}
	if s3Region != "" {
		props["s3.region"] = s3Region
	}
	if catalogToken != "" {
		props["token"] = catalogToken
	}

	cat, err := catalog.Load(ctx, "rest", props)
	if err != nil {
		return nil, fmt.Errorf("loading iceberg catalog: %w", err)
	}
	return &IcebergWriter{cat: cat}, nil
}

// ensureTable loads circa.metrics, creating its namespace and the table
// itself (partitioned by day(ts), node_id) on first use. Safe to call on
// every export tick — CreateNamespace's "already exists" error is expected
// steady-state, not a real failure, and is swallowed rather than checked
// against a specific sentinel, since a REST catalog's HTTP-derived error
// isn't guaranteed to wrap catalog.ErrNamespaceAlreadyExists exactly.
func (w *IcebergWriter) ensureTable(ctx context.Context) (*table.Table, error) {
	ns := table.Identifier{tableNamespace}
	_ = w.cat.CreateNamespace(ctx, ns, nil)

	ident := table.Identifier{tableNamespace, tableName}
	if tbl, err := w.cat.LoadTable(ctx, ident); err == nil {
		return tbl, nil
	}

	spec := iceberg.NewPartitionSpec(
		iceberg.PartitionField{SourceIDs: []int{5}, FieldID: 1000, Transform: iceberg.DayTransform{}, Name: "ts_day"},
		iceberg.PartitionField{SourceIDs: []int{1}, FieldID: 1001, Transform: iceberg.IdentityTransform{}, Name: "node_id"},
	)
	return w.cat.CreateTable(ctx, ident, icebergSchema, catalog.WithPartitionSpec(&spec))
}

// Append writes rows as one new Iceberg snapshot. A no-op for an empty
// slice, so callers never need to special-case "nothing new since the
// last watermark."
func (w *IcebergWriter) Append(ctx context.Context, rows []Row) error {
	if len(rows) == 0 {
		return nil
	}

	tbl, err := w.ensureTable(ctx)
	if err != nil {
		return fmt.Errorf("ensuring iceberg table: %w", err)
	}

	arrSchema, err := table.SchemaToArrowSchema(icebergSchema, nil, true, false)
	if err != nil {
		return fmt.Errorf("converting schema: %w", err)
	}

	pool := memory.NewGoAllocator()
	bldr := array.NewRecordBuilder(pool, arrSchema)
	defer bldr.Release()

	for _, r := range rows {
		bldr.Field(0).(*array.StringBuilder).Append(r.NodeID)
		bldr.Field(1).(*array.StringBuilder).Append(r.Hostname)
		bldr.Field(2).(*array.StringBuilder).Append(r.MetricName)
		bldr.Field(3).(*array.StringBuilder).Append(labelsJSON(r.Labels))
		bldr.Field(4).(*array.TimestampBuilder).Append(arrow.Timestamp(r.Time.UnixMicro()))
		bldr.Field(5).(*array.Float64Builder).Append(r.Value)
		bldr.Field(6).(*array.BooleanBuilder).Append(r.Anomalous)
	}

	rec := bldr.NewRecord()
	defer rec.Release()

	arrTbl := array.NewTableFromRecords(arrSchema, []arrow.Record{rec})
	defer arrTbl.Release()

	if _, err := tbl.AppendTable(ctx, arrTbl, int64(len(rows)), nil); err != nil {
		return fmt.Errorf("appending to iceberg table: %w", err)
	}
	return nil
}
