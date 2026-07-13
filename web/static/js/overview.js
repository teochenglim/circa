// "Overall" tab (v0.6.0): a Netdata-style zero-config grid of small charts,
// auto-populated from whatever internal/collect (v0.5.0) produced — no
// scrape target, no metric selection, nothing to configure. Grouped by the
// fixed collector categories (cpu/memory/network/disk/filesystem/load),
// not by scrape target/measurement the way the manual "Metrics" tab is
// (see RELEASE/v0.6.0.md: this is a second, additional view, not a
// replacement). Compact/aggregate by design — see detail.js for the
// bigger, per-device/per-core view each category gets its own tab for.
//
// The Overall tab (and the per-category tabs, see tabs.js) hides itself
// entirely when no node_* series exist yet (features.collect off, an
// unsupported platform, or just-started with nothing collected yet)
// rather than showing empty/broken charts.
(function () {
  "use strict";

  var REFRESH_MS = 15000;
  var D = window.CircaData;

  var charts = {}; // container id -> uPlot instance, so a refresh destroys the previous one cleanly

  function el(id) {
    return document.getElementById(id);
  }

  function drawChart(containerId, uPlotSeries, data) {
    var container = el(containerId);
    if (!container) return;
    if (charts[containerId]) {
      charts[containerId].destroy();
      charts[containerId] = null;
    }
    if (!data[0] || data[0].length === 0) {
      container.innerHTML = '<p class="panel-empty">No data yet.</p>';
      return;
    }
    container.innerHTML = "";
    charts[containerId] = new uPlot(
      {
        width: Math.max(container.clientWidth || 260, 200),
        height: 140,
        series: uPlotSeries,
        scales: { x: { time: true } },
        legend: window.CircaChart.legendOpts({ show: uPlotSeries.length > 2 }),
        cursor: { show: true },
      },
      data,
      container
    );
    window.CircaChart.bindShowAll(charts[containerId]);
  }

  // loadCPU shows one "busy %" line either way, computed differently per
  // platform since the underlying metric shapes are deliberately different
  // (RELEASE/v0.5.0.md): Linux exposes a real per-mode counter, so busy% =
  // 100 - avg(rate(idle)) across CPUs; macOS exposes an instantaneous
  // gauge already in %, so busy% = 100 - idle directly, no rate needed.
  function loadCPU(names) {
    var id = "overview-cpu";
    if (names.indexOf("node_cpu_seconds_total") !== -1) {
      D.queryRange("node_cpu_seconds_total", "mode=idle").then(function (idleSeries) {
        if (idleSeries.length === 0) return drawChart(id, [], [[]]);
        var totals = {}, counts = {};
        idleSeries.forEach(function (s) {
          D.rateOf(s.values || []).forEach(function (pt) {
            totals[pt[0]] = (totals[pt[0]] || 0) + pt[1];
            counts[pt[0]] = (counts[pt[0]] || 0) + 1;
          });
        });
        var xs = Object.keys(totals).map(Number).sort(function (a, b) { return a - b; });
        var busy = xs.map(function (x) {
          return Math.max(0, Math.min(100, 100 - (totals[x] / counts[x]) * 100));
        });
        drawChart(id, [{}, { label: "busy %", stroke: D.categoryColor("cpu"), width: 2 }], [xs, busy]);
      });
    } else if (names.indexOf("node_cpu_usage_percent") !== -1) {
      D.queryRange("node_cpu_usage_percent", "mode=idle").then(function (result) {
        if (result.length === 0) return drawChart(id, [], [[]]);
        var xy = D.pointsToXY(result[0].values || []);
        var busy = xy[1].map(function (idle) { return 100 - idle; });
        drawChart(id, [{}, { label: "busy %", stroke: D.categoryColor("cpu"), width: 2 }], [xy[0], busy]);
      });
    } else {
      drawChart(id, [], [[]]);
    }
  }

  // loadMemory picks whichever total/available-ish metric pair actually
  // exists — Linux's node_memory_MemTotal_bytes/MemAvailable_bytes or
  // macOS's node_memory_total_bytes/free_bytes (deliberately different
  // vocabulary per platform, see RELEASE/v0.5.0.md's memory metric note;
  // macOS has no single Linux-style "available" number, so this is a
  // cruder free-vs-total approximation there, not a precision claim).
  function loadMemory(names) {
    var id = "overview-memory";
    var totalMetric, spareMetric;
    if (names.indexOf("node_memory_MemTotal_bytes") !== -1) {
      totalMetric = "node_memory_MemTotal_bytes";
      spareMetric = "node_memory_MemAvailable_bytes";
    } else if (names.indexOf("node_memory_total_bytes") !== -1) {
      totalMetric = "node_memory_total_bytes";
      spareMetric = "node_memory_free_bytes";
    } else {
      return drawChart(id, [], [[]]);
    }
    Promise.all([D.queryRange(totalMetric), D.queryRange(spareMetric)]).then(function (r) {
      var totalResult = r[0], spareResult = r[1];
      if (totalResult.length === 0 || spareResult.length === 0) return drawChart(id, [], [[]]);
      var totalPts = {}, sparePts = {};
      (totalResult[0].values || []).forEach(function (pt) { totalPts[pt[0]] = parseFloat(pt[1]); });
      (spareResult[0].values || []).forEach(function (pt) { sparePts[pt[0]] = parseFloat(pt[1]); });
      var xs = Object.keys(totalPts).map(Number).filter(function (x) { return x in sparePts; }).sort(function (a, b) { return a - b; });
      var usedPct = xs.map(function (x) { return Math.max(0, Math.min(100, 100 * (1 - sparePts[x] / totalPts[x]))); });
      drawChart(id, [{}, { label: "used %", stroke: D.categoryColor("memory"), width: 2 }], [xs, usedPct]);
    });
  }

  // loadNetwork shows only the primary interface (whichever one is
  // actually carrying the default route, per node_network_primary_info —
  // not guessed by name, since "the Wi-Fi/USB adapter" has no fixed name
  // across machines) rather than every device — the Network tab (detail.js)
  // shows every device.
  function loadNetwork(seriesList) {
    var id = "overview-network";
    var primary = seriesList.filter(function (s) { return s.name === "node_network_primary_info"; })[0];
    var device = primary && primary.labels ? primary.labels.device : null;
    if (!device) return drawChart(id, [], [[]]);
    var filter = "device=" + device;
    Promise.all([
      D.queryRange("node_network_receive_bytes_total", filter),
      D.queryRange("node_network_transmit_bytes_total", filter),
    ]).then(function (r) {
      var rx = D.sumRates(r[0]), tx = D.sumRates(r[1]);
      if (rx[0].length === 0 && tx[0].length === 0) return drawChart(id, [], [[]]);
      drawChart(
        id,
        [{}, { label: "rx B/s", stroke: D.categoryColor("network"), width: 2 }, { label: "tx B/s", stroke: "#94a3b8", width: 2 }],
        D.mergeOnX(rx, tx)
      );
    });
  }

  // loadDisk sums across every device (Linux: real per-device counters;
  // macOS: a single device="total" aggregate, see RELEASE/v0.5.0.md) — the
  // Disk tab (detail.js) breaks this out per device.
  function loadDisk(names) {
    var id = "overview-disk";
    if (names.indexOf("node_disk_read_bytes_total") === -1) return drawChart(id, [], [[]]);
    Promise.all([D.queryRange("node_disk_read_bytes_total"), D.queryRange("node_disk_written_bytes_total")]).then(function (r) {
      var read = D.sumRates(r[0]), written = D.sumRates(r[1]);
      if (read[0].length === 0 && written[0].length === 0) return drawChart(id, [], [[]]);
      drawChart(
        id,
        [{}, { label: "read B/s", stroke: D.categoryColor("disk"), width: 2 }, { label: "write B/s", stroke: "#94a3b8", width: 2 }],
        D.mergeOnX(read, written)
      );
    });
  }

  // loadFilesystem shows only the root mount ("/") — the Filesystem tab
  // (detail.js) shows every mounted filesystem.
  function loadFilesystem(names) {
    var id = "overview-filesystem";
    if (names.indexOf("node_filesystem_avail_bytes") === -1) return drawChart(id, [], [[]]);
    Promise.all([
      D.queryRange("node_filesystem_size_bytes", "mountpoint=/"),
      D.queryRange("node_filesystem_avail_bytes", "mountpoint=/"),
    ]).then(function (r) {
      var sizeResult = r[0], availResult = r[1];
      if (sizeResult.length === 0 || availResult.length === 0) return drawChart(id, [], [[]]);
      var sizePts = {}, availPts = {};
      (sizeResult[0].values || []).forEach(function (pt) { sizePts[pt[0]] = parseFloat(pt[1]); });
      (availResult[0].values || []).forEach(function (pt) { availPts[pt[0]] = parseFloat(pt[1]); });
      var xs = Object.keys(sizePts).map(Number).filter(function (x) { return x in availPts; }).sort(function (a, b) { return a - b; });
      var usedPct = xs.map(function (x) { return Math.max(0, Math.min(100, 100 * (1 - availPts[x] / sizePts[x]))); });
      drawChart(id, [{}, { label: "/ used %", stroke: D.categoryColor("filesystem"), width: 2 }], [xs, usedPct]);
    });
  }

  function loadLoad(names) {
    var id = "overview-load";
    if (names.indexOf("node_load1") === -1) return drawChart(id, [], [[]]);
    Promise.all([D.queryRange("node_load1"), D.queryRange("node_load5"), D.queryRange("node_load15")]).then(function (r) {
      var l1 = r[0], l5 = r[1], l15 = r[2];
      if (l1.length === 0) return drawChart(id, [], [[]]);

      // Folds each extra metric in via mergeOnX in turn, so every
      // already-merged series gets re-aligned onto the progressively
      // widened x-axis each time — not just the newest one (see mergeOnX's
      // own doc comment for why that distinction matters).
      var data = D.pointsToXY(l1[0].values || []);
      var series = [{}, { label: "1m", stroke: D.categoryColor("load"), width: 2 }];
      [
        { result: l5, label: "5m", stroke: "#94a3b8" },
        { result: l15, label: "15m", stroke: "#cbd5e1" },
      ].forEach(function (extra) {
        series.push({ label: extra.label, stroke: extra.stroke, width: 1 });
        var extraXY = extra.result.length ? D.pointsToXY(extra.result[0].values || []) : [[], []];
        data = D.mergeOnX(data, extraXY);
      });
      drawChart(id, series, data);
    });
  }

  function loadOverview() {
    D.fetchJSON("/api/v1/series")
      .then(function (body) {
        var seriesList = body.data || [];
        var names = seriesList.map(function (s) { return s.name; });
        var section = el("overview");
        if (!section) return;

        var hasAny = names.some(function (n) { return n.indexOf("node_") === 0; });
        if (!hasAny) {
          section.innerHTML = '<p class="panel-empty">No local system metrics yet — features.collect may be off, or this platform isn\'t supported yet (see RELEASE/v1.1.0.md).</p>';
          return;
        }

        loadCPU(names);
        loadMemory(names);
        loadNetwork(seriesList);
        loadDisk(names);
        loadFilesystem(names);
        loadLoad(names);
      })
      .catch(function () {
        // Best-effort: a failed /api/v1/series fetch leaves the section as
        // it was rather than tearing down the rest of the dashboard.
      });
  }

  loadOverview();
  setInterval(loadOverview, REFRESH_MS);
})();
