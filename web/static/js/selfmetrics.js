// Self-metrics page (v1.0.0): visualizes circa's own RED self-metrics
// (ARCHITECTURE.md "Self-metrics", registered v0.7.0) — the "lightweight
// self-metrics panel" DESIGN/05 always called for but only shipped as a
// text endpoint (GET /metrics) until now.
//
// Deliberately different plumbing than overview.js/detail.js: those read
// history back out of internal/storage via /api/v1/query_range, but
// self-metrics were never written there (see ARCHITECTURE.md's "Built
// differently than originally planned" note on GET /metrics) — there is no
// stored time series to query. Instead this polls the live snapshot at
// GET /api/v1/selfmetrics (selfmetrics_api.go) every REFRESH_MS and builds
// its own short rolling history entirely in the browser. Every number here
// is real, current process state at the moment it was polled — never
// simulated (see RELEASE/v1.0.0.md's explicit note on that point) — it's
// just not persisted anywhere once the tab closes.
(function () {
  "use strict";

  var REFRESH_MS = 5000;
  var HISTORY_LIMIT = 120; // ~10 minutes of browser-side history at the interval above
  var D = window.CircaData;
  var charts = {};

  // series: key -> { labels, points: [[epochSeconds, value], ...] } — one
  // entry per distinct name+label-set circa's /api/v1/selfmetrics has ever
  // reported, capped to the last HISTORY_LIMIT polls each.
  var series = {};

  function el(id) {
    return document.getElementById(id);
  }

  function keyOf(name, labels) {
    var parts = [];
    for (var k in labels || {}) parts.push(k + "=" + labels[k]);
    parts.sort();
    return name + "|" + parts.join(",");
  }

  function record(now, points) {
    points.forEach(function (p) {
      var key = keyOf(p.name, p.labels);
      var s = series[key] || (series[key] = { name: p.name, labels: p.labels || {}, points: [] });
      s.points.push([now, p.value]);
      if (s.points.length > HISTORY_LIMIT) s.points.shift();
    });
  }

  function byName(name) {
    return Object.keys(series)
      .map(function (k) { return series[k]; })
      .filter(function (s) { return s.name === name; });
  }

  function latestSum(name) {
    return byName(name).reduce(function (total, s) {
      return total + (s.points.length ? s.points[s.points.length - 1][1] : 0);
    }, 0);
  }

  function drawChart(containerId, uPlotSeries, data) {
    var container = el(containerId);
    if (!container) return;
    if (charts[containerId]) {
      charts[containerId].destroy();
      charts[containerId] = null;
    }
    if (!data[0] || data[0].length < 2) {
      container.innerHTML = '<p class="panel-empty">Collecting…</p>';
      return;
    }
    container.innerHTML = "";
    charts[containerId] = new uPlot(
      {
        width: Math.max(container.clientWidth || 280, 240),
        height: 150,
        series: uPlotSeries,
        scales: { x: { time: true } },
        legend: window.CircaChart.legendOpts(),
        cursor: { show: true },
      },
      data,
      container
    );
    window.CircaChart.bindShowAll(charts[containerId]);
  }

  // totalRateChart draws one line: the summed rate() across every
  // label-combination of `name`, optionally narrowed by predicate(labels).
  function totalRateChart(containerId, name, label, predicate) {
    var entries = byName(name).filter(function (s) { return !predicate || predicate(s.labels); });
    var xy = D.sumRates(entries.map(function (s) { return { values: s.points }; }));
    drawChart(containerId, [{}, { label: label, stroke: D.palette[0], width: 2 }], xy);
  }

  // labeledRateChart draws one rate() line per distinct label-combination —
  // the self-metrics analogue of detail.js's perDeviceRateChart, for a
  // counter whose "result"/"target"/"notifier" label is the interesting
  // dimension rather than something worth collapsing into one total.
  function labeledRateChart(containerId, name, preferKey) {
    var lines = byName(name)
      .map(function (s) { return { xy: D.rateOfXY(s.points), label: D.labelFor(s.labels, preferKey) }; })
      .filter(function (l) { return l.xy[0].length > 0; });
    var uSeries = [{}].concat(lines.map(function (l, i) { return { label: l.label, stroke: D.palette[i % D.palette.length], width: 2 }; }));
    drawChart(containerId, uSeries, lines.length ? D.mergeAllOnX(lines.map(function (l) { return l.xy; })) : [[]]);
  }

  // avgChart draws avg(baseName) = rate(baseName_sum) / rate(baseName_count)
  // — a histogram's own running average latency, in milliseconds, the same
  // derivation a PromQL `rate(x_sum[w]) / rate(x_count[w])` does.
  function avgMsChart(containerId, baseName, label) {
    var sumEntries = byName(baseName + "_sum").map(function (s) { return { values: s.points }; });
    var countEntries = byName(baseName + "_count").map(function (s) { return { values: s.points }; });
    var sumXY = D.sumRates(sumEntries);
    var countXY = D.sumRates(countEntries);

    var byX = {};
    countXY[0].forEach(function (x, i) { byX[x] = countXY[1][i]; });
    var xs = [], ys = [];
    sumXY[0].forEach(function (x, i) {
      if (byX[x]) {
        xs.push(x);
        ys.push(byX[x] > 0 ? 1000 * sumXY[1][i] / byX[x] : 0);
      }
    });
    drawChart(containerId, [{}, { label: label, stroke: D.palette[1], width: 2 }], [xs, ys]);
  }

  function statTile(id, text) {
    var e = el(id);
    if (e) e.textContent = text;
  }

  function humanBytes(n) {
    var units = ["B", "KiB", "MiB", "GiB"];
    var i = 0;
    while (n >= 1024 && i < units.length - 1) {
      n /= 1024;
      i++;
    }
    return n.toFixed(1) + units[i];
  }

  function refresh() {
    D.fetchJSON("/api/v1/selfmetrics").then(function (body) {
      if (body.status !== "success") return;
      record(Math.floor(Date.now() / 1000), body.data || []);

      statTile("stat-goroutines", latestSum("go_goroutines").toFixed(0));
      statTile("stat-heap", humanBytes(latestSum("go_memstats_heap_alloc_bytes")));

      totalRateChart("chart-http-requests", "circa_http_requests_total", "requests/s");
      totalRateChart("chart-http-errors", "circa_http_requests_total", "5xx/s", function (l) { return (l.status || "")[0] === "5"; });
      avgMsChart("chart-http-latency", "circa_http_request_duration_seconds", "avg latency (ms)");

      totalRateChart("chart-storage-writes", "circa_storage_writes_total", "writes/s");
      totalRateChart("chart-storage-errors", "circa_storage_write_errors_total", "errors/s");

      labeledRateChart("chart-collect-ticks", "circa_collect_ticks_total", "result");
      totalRateChart("chart-collect-samples", "circa_collect_samples_total", "samples/s");

      labeledRateChart("chart-scrape-requests", "circa_scrape_requests_total", "target");
      labeledRateChart("chart-scrape-errors", "circa_scrape_errors_total", "target");

      totalRateChart("chart-alert-evaluations", "circa_alert_evaluations_total", "evaluations/s");
      labeledRateChart("chart-alert-notify", "circa_alert_notify_total", "result");

      totalRateChart("chart-anomaly-scores", "circa_anomaly_scores_total", "scores/s");
      labeledRateChart("chart-anomaly-retrains", "circa_anomaly_retrains_total", "result");

      labeledRateChart("chart-backup-exports", "circa_backup_exports_total", "result");
      totalRateChart("chart-backup-rows", "circa_backup_rows_exported_total", "rows/s");

      totalRateChart("chart-remotewrite-receive", "circa_remotewrite_receive_samples_total", "samples/s");
      labeledRateChart("chart-remotewrite-send", "circa_remotewrite_send_total", "result");
    });
  }

  refresh();
  setInterval(refresh, REFRESH_MS);
})();
