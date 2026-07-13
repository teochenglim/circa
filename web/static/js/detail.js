// Per-category detail pages (v0.6.0): CPU/Memory/Network/Disk/Filesystem/
// Load each get their own page (cpu.html, memory.html, ...), bigger and
// more detailed than the "Overall" page's compact grid (overview.js) —
// every device/core/mountpoint gets its own line here, not just the
// primary/aggregate/root one. Which category to render is read from
// <body data-category="..."> rather than duplicating this file six times.
(function () {
  "use strict";

  var REFRESH_MS = 15000;
  var D = window.CircaData;
  var category = document.body.getAttribute("data-category");

  var charts = {}; // container id -> uPlot instance

  function el(id) {
    return document.getElementById(id);
  }

  function drawChart(containerId, uPlotSeries, data) {
    var container = el(containerId);
    if (!container) return;
    container.style.display = "";
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
        width: Math.max(container.clientWidth || 600, 300),
        height: 320,
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

  function noData(containerId) {
    var container = el(containerId);
    if (container) container.innerHTML = '<p class="panel-empty">No local system metrics for this category yet — features.collect may be off, or nothing has been collected yet.</p>';
  }

  // perDeviceRateChart draws one rate() line per series returned for a
  // counter metric (one per device/interface, unfiltered — unlike the
  // Overall page's primary-device-only / summed view).
  function perDeviceRateChart(containerId, resultSeries, labelKey) {
    if (resultSeries.length === 0) return noData(containerId);
    var xyList = resultSeries.map(function (s) { return D.rateOfXY(s.values || []); });
    var series = [{}].concat(resultSeries.map(function (s, i) {
      return { label: D.labelFor(s.metric, labelKey), stroke: D.palette[i % D.palette.length], width: 2 };
    }));
    drawChart(containerId, series, D.mergeAllOnX(xyList));
  }

  // loadCPU shows one busy% line per CPU core on Linux (real per-core
  // counters), or the three raw mode percentages on macOS (no per-core
  // breakdown available without cgo — see RELEASE/v0.5.0.md).
  function loadCPU() {
    D.queryRange("node_cpu_seconds_total").then(function (all) {
      if (all.length > 0) {
        var idleByCPU = {};
        all.forEach(function (s) {
          if (s.metric.mode === "idle") idleByCPU[s.metric.cpu] = s;
        });
        var cpus = Object.keys(idleByCPU).sort(function (a, b) { return Number(a) - Number(b); });
        if (cpus.length === 0) return noData("detail-chart-1");
        var xyList = cpus.map(function (cpu) {
          var rate = D.rateOfXY(idleByCPU[cpu].values || []);
          return [rate[0], rate[1].map(function (idleFrac) { return Math.max(0, Math.min(100, 100 - idleFrac * 100)); })];
        });
        var series = [{}].concat(cpus.map(function (cpu, i) {
          return { label: "cpu" + cpu, stroke: D.palette[i % D.palette.length], width: 2 };
        }));
        drawChart("detail-chart-1", series, D.mergeAllOnX(xyList));
        return;
      }
      D.queryRange("node_cpu_usage_percent").then(function (result) {
        if (result.length === 0) return noData("detail-chart-1");
        var xyList = result.map(function (s) { return D.pointsToXY(s.values || []); });
        var series = [{}].concat(result.map(function (s, i) {
          return { label: s.metric.mode, stroke: D.palette[i % D.palette.length], width: 2 };
        }));
        drawChart("detail-chart-1", series, D.mergeAllOnX(xyList));
      });
    });
  }

  // loadMemory plots every node_memory_* series in raw bytes, whatever set
  // this platform produced (Linux's MemTotal/MemFree/MemAvailable/... or
  // macOS's total/free/active/inactive/wired/compressed — see
  // RELEASE/v0.5.0.md's memory metric note) — the Overall page's single
  // used% is a derived summary of a subset of these.
  function loadMemory() {
    D.fetchJSON("/api/v1/series").then(function (body) {
      var names = (body.data || []).map(function (s) { return s.name; }).filter(function (n) { return n.indexOf("node_memory_") === 0; });
      var uniqueNames = names.filter(function (n, i) { return names.indexOf(n) === i; }).sort();
      if (uniqueNames.length === 0) return noData("detail-chart-1");
      Promise.all(uniqueNames.map(function (n) { return D.queryRange(n); })).then(function (results) {
        var series = [{}];
        var xyList = [];
        results.forEach(function (result, i) {
          if (result.length === 0) return;
          series.push({
            label: uniqueNames[i].replace("node_memory_", "").replace(/_bytes$/, ""),
            stroke: D.palette[xyList.length % D.palette.length],
            width: 2,
          });
          xyList.push(D.pointsToXY(result[0].values || []));
        });
        if (xyList.length === 0) return noData("detail-chart-1");
        drawChart("detail-chart-1", series, D.mergeAllOnX(xyList));
      });
    });
  }

  // loadNetwork/loadDisk show every device across two charts (receive/
  // transmit, read/write) — the Overall page narrows network to the
  // primary interface and sums disk across devices; this page is the
  // "show me everything" drill-down, including the USB/secondary
  // interfaces the primary-only Overall view deliberately omits.
  function loadNetwork() {
    Promise.all([
      D.queryRange("node_network_receive_bytes_total"),
      D.queryRange("node_network_transmit_bytes_total"),
    ]).then(function (r) {
      perDeviceRateChart("detail-chart-1", r[0], "device");
      perDeviceRateChart("detail-chart-2", r[1], "device");
    });
  }

  function loadDisk() {
    Promise.all([
      D.queryRange("node_disk_read_bytes_total"),
      D.queryRange("node_disk_written_bytes_total"),
    ]).then(function (r) {
      perDeviceRateChart("detail-chart-1", r[0], "device");
      perDeviceRateChart("detail-chart-2", r[1], "device");
    });
  }

  // loadFilesystem shows used% for every mounted filesystem circa can see
  // (not just "/"), pairing each mountpoint's size/avail series by label.
  function loadFilesystem() {
    Promise.all([
      D.queryRange("node_filesystem_size_bytes"),
      D.queryRange("node_filesystem_avail_bytes"),
    ]).then(function (r) {
      var sizeSeries = r[0], availSeries = r[1];
      if (sizeSeries.length === 0) return noData("detail-chart-1");

      var byMount = {};
      sizeSeries.forEach(function (s) { byMount[s.metric.mountpoint] = { size: s }; });
      availSeries.forEach(function (s) {
        if (byMount[s.metric.mountpoint]) byMount[s.metric.mountpoint].avail = s;
      });
      var mounts = Object.keys(byMount).filter(function (m) { return byMount[m].avail; }).sort();
      if (mounts.length === 0) return noData("detail-chart-1");

      var xyList = mounts.map(function (m) {
        var sizePts = {}, availPts = {};
        (byMount[m].size.values || []).forEach(function (pt) { sizePts[pt[0]] = parseFloat(pt[1]); });
        (byMount[m].avail.values || []).forEach(function (pt) { availPts[pt[0]] = parseFloat(pt[1]); });
        var xs = Object.keys(sizePts).map(Number).filter(function (x) { return x in availPts; }).sort(function (a, b) { return a - b; });
        return [xs, xs.map(function (x) { return Math.max(0, Math.min(100, 100 * (1 - availPts[x] / sizePts[x]))); })];
      });
      var series = [{}].concat(mounts.map(function (m, i) {
        return { label: m, stroke: D.palette[i % D.palette.length], width: 2 };
      }));
      drawChart("detail-chart-1", series, D.mergeAllOnX(xyList));
    });
  }

  function loadLoad() {
    Promise.all([D.queryRange("node_load1"), D.queryRange("node_load5"), D.queryRange("node_load15")]).then(function (r) {
      if (r[0].length === 0) return noData("detail-chart-1");
      var labels = ["1m", "5m", "15m"];
      var xyList = r.map(function (result) {
        return result.length ? D.pointsToXY(result[0].values || []) : [[], []];
      });
      var series = [{}].concat(labels.map(function (l, i) {
        return { label: l, stroke: D.palette[i % D.palette.length], width: 2 };
      }));
      drawChart("detail-chart-1", series, D.mergeAllOnX(xyList));
    });
  }

  var loaders = {
    cpu: loadCPU,
    memory: loadMemory,
    network: loadNetwork,
    disk: loadDisk,
    filesystem: loadFilesystem,
    load: loadLoad,
  };

  function load() {
    var fn = loaders[category];
    if (fn) fn();
  }

  load();
  setInterval(load, REFRESH_MS);
})();
