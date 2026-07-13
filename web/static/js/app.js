// app.js: the "Metrics" page (v0.6.0 renamed this from the whole
// dashboard's only view to one tab among several — see index.html/cpu.html/
// etc. and detail.js/overview.js for the others) — manual metric picker +
// the existing Alerts/"what's unusual" panels. Depends on circa-chart.js
// (window.CircaChart) being loaded first, same as every other page's chart.
(function () {
  "use strict";

  var metricSelect = document.getElementById("metric");
  var rangeSelect = document.getElementById("range");
  var autoRefresh = document.getElementById("autorefresh");
  var statusEl = document.getElementById("status");
  var chartEl = document.getElementById("chart");

  var plot = null;
  var refreshTimer = null;

  function setStatus(msg) {
    statusEl.textContent = msg || "";
  }

  // Blanks the chart immediately — called right when the user switches
  // metric/range (before the new query even resolves) so a stale previous
  // chart is never left on screen, and again on any query error.
  function clearChart() {
    if (plot) {
      plot.destroy();
      plot = null;
    }
    chartEl.innerHTML = "";
  }

  // Coarser ranges read from the minute/hour rollup tiers instead of raw —
  // matches the retention those tiers are actually sized for (DESIGN/03).
  function tierForRangeSeconds(rangeSec) {
    if (rangeSec <= 3600) return "raw";
    if (rangeSec <= 7 * 24 * 3600) return "minute";
    return "hour";
  }

  function labelString(metric) {
    var parts = [];
    for (var k in metric) {
      if (k !== "__name__" && Object.prototype.hasOwnProperty.call(metric, k)) {
        parts.push(k + "=" + metric[k]);
      }
    }
    return parts.length ? parts.join(",") : (metric.__name__ || "value");
  }

  function loadMetrics() {
    fetch("/api/v1/series")
      .then(function (r) { return r.json(); })
      .then(function (body) {
        var names = [];
        var seen = {};
        (body.data || []).forEach(function (s) {
          if (!seen[s.name]) {
            seen[s.name] = true;
            names.push(s.name);
          }
        });
        names.sort();

        metricSelect.innerHTML = "";
        if (names.length === 0) {
          var opt = document.createElement("option");
          opt.value = "";
          opt.textContent = "(no metrics ingested yet)";
          metricSelect.appendChild(opt);
          setStatus("No series yet — check ingest.scrape.targets in config.yaml.");
          return;
        }
        names.forEach(function (name) {
          var opt = document.createElement("option");
          opt.value = name;
          opt.textContent = name;
          metricSelect.appendChild(opt);
        });
        loadAndRender();
      })
      .catch(function (err) {
        setStatus("Failed to load metric list: " + err);
      });
  }

  // Builds a shared x-axis (the sorted union of every returned series'
  // timestamps) and aligns each series onto it, since different scrape
  // targets don't necessarily share exact tick times.
  function alignSeries(resultSeries, valueKey) {
    var xSet = {};
    resultSeries.forEach(function (s) {
      (s.values || []).forEach(function (pt) { xSet[pt[0]] = true; });
    });
    var xs = Object.keys(xSet).map(Number).sort(function (a, b) { return a - b; });
    var xIndex = {};
    xs.forEach(function (x, i) { xIndex[x] = i; });

    var seriesData = resultSeries.map(function (s) {
      var arr = new Array(xs.length).fill(null);
      (s[valueKey] || s.values || []).forEach(function (pt) {
        var idx = xIndex[pt[0]];
        if (idx !== undefined) arr[idx] = parseFloat(pt[1]);
      });
      return arr;
    });

    return { xs: xs, seriesData: seriesData };
  }

  function render(result, tier) {
    clearChart();

    var resultSeries = result.data && result.data.result ? result.data.result : [];
    if (resultSeries.length === 0) {
      setStatus("No data in this range yet.");
      return;
    }
    setStatus(resultSeries.length + " series");

    var uPlotSeries = [{}]; // x-axis placeholder
    var data;

    if (resultSeries.length === 1 && tier !== "raw") {
      // Single series with a tier that has min/avg/max: show the band.
      var aligned = alignSeries(resultSeries, "values");
      var mins = alignSeries(resultSeries, "min").seriesData[0];
      var maxes = alignSeries(resultSeries, "max").seriesData[0];
      uPlotSeries.push({ label: "min", stroke: "#999", width: 1 });
      uPlotSeries.push({ label: labelString(resultSeries[0].metric), stroke: "#2b6cb0", width: 2 });
      uPlotSeries.push({ label: "max", stroke: "#999", width: 1 });
      data = [aligned.xs, mins, aligned.seriesData[0], maxes];
    } else {
      var alignedMulti = alignSeries(resultSeries, "values");
      var palette = ["#2b6cb0", "#c05621", "#2f855a", "#6b46c1", "#b7791f", "#c53030"];
      resultSeries.forEach(function (s, i) {
        uPlotSeries.push({ label: labelString(s.metric), stroke: palette[i % palette.length], width: 2 });
      });
      data = [alignedMulti.xs].concat(alignedMulti.seriesData);
    }

    plot = new uPlot({
      width: Math.min(chartEl.clientWidth || 900, 1100),
      height: 400,
      series: uPlotSeries,
      scales: { x: { time: true } },
      legend: window.CircaChart.legendOpts(),
    }, data, chartEl);
    window.CircaChart.bindShowAll(plot);
  }

  function loadAndRender() {
    var metric = metricSelect.value;
    if (!metric) return;

    var rangeSec = parseInt(rangeSelect.value, 10);
    var tier = tierForRangeSeconds(rangeSec);
    var end = Math.floor(Date.now() / 1000);
    var start = end - rangeSec;

    var url = "/api/v1/query_range?metric=" + encodeURIComponent(metric) +
      "&start=" + start + "&end=" + end + "&tier=" + tier;

    fetch(url)
      .then(function (r) { return r.json(); })
      .then(function (body) {
        if (body.status !== "success") {
          clearChart();
          setStatus("Query failed: " + body.error);
          return;
        }
        render(body, tier);
      })
      .catch(function (err) {
        clearChart();
        setStatus("Failed to load data: " + err);
      });
  }

  // Bound to the metric/range selects: clears the chart immediately (rather
  // than leaving the previous metric's chart on screen until the new query
  // resolves) before kicking off the new query. Auto-refresh reuses
  // loadAndRender directly, without this preemptive clear, so a routine
  // refresh doesn't flash the chart blank every cycle.
  function onSelectionChange() {
    clearChart();
    setStatus("Loading…");
    loadAndRender();
  }

  var alertsBody = document.getElementById("alerts-body");
  var anomaliesBody = document.getElementById("anomalies-body");

  function escapeHTML(s) {
    var div = document.createElement("div");
    div.textContent = s;
    return div.innerHTML;
  }

  // loadAlerts populates the Alerts panel from GET /api/v1/alerts — always
  // present (empty when features.alerts is off), so this doesn't need to
  // know whether alerting is even enabled.
  function loadAlerts() {
    fetch("/api/v1/alerts")
      .then(function (r) { return r.json(); })
      .then(function (body) {
        var alerts = (body.data || []);
        if (alerts.length === 0) {
          alertsBody.innerHTML = '<p class="panel-empty">No alerts firing.</p>';
          return;
        }
        var rows = alerts.map(function (a) {
          return "<tr>" +
            '<td class="severity-' + escapeHTML(a.severity) + '">' + escapeHTML(a.severity) + "</td>" +
            "<td>" + escapeHTML(a.rule) + "</td>" +
            "<td>" + escapeHTML(a.metric) + labelSuffix(a.labels) + "</td>" +
            "<td>" + escapeHTML(String(a.value)) + "</td>" +
            "<td>" + new Date(a.since).toLocaleTimeString() + "</td>" +
            "</tr>";
        }).join("");
        alertsBody.innerHTML =
          "<table><thead><tr><th>Severity</th><th>Rule</th><th>Metric</th><th>Value</th><th>Since</th></tr></thead>" +
          "<tbody>" + rows + "</tbody></table>";
      })
      .catch(function () {
        alertsBody.innerHTML = '<p class="panel-empty">Failed to load alerts.</p>';
      });
  }

  // loadAnomalies populates the "what's unusual" panel from
  // GET /api/v1/anomalies — the ranked list DESIGN/06 §6.2 calls for.
  // Always present (empty when features.ml is off or nothing's anomalous).
  function loadAnomalies() {
    fetch("/api/v1/anomalies?window=600")
      .then(function (r) { return r.json(); })
      .then(function (body) {
        var ranks = (body.data || []);
        if (ranks.length === 0) {
          anomaliesBody.innerHTML = '<p class="panel-empty">Nothing unusual right now.</p>';
          return;
        }
        var rows = ranks.map(function (r) {
          return "<tr>" +
            "<td>" + escapeHTML(r.Metric.__name__ || "") + labelSuffix(r.Metric) + "</td>" +
            "<td>" + (r.Rate * 100).toFixed(0) + "%</td>" +
            "<td>" + r.Count + "</td>" +
            "</tr>";
        }).join("");
        anomaliesBody.innerHTML =
          "<table><thead><tr><th>Metric</th><th>Anomaly rate</th><th>Points</th></tr></thead>" +
          "<tbody>" + rows + "</tbody></table>";
      })
      .catch(function () {
        anomaliesBody.innerHTML = '<p class="panel-empty">Failed to load anomalies.</p>';
      });
  }

  function labelSuffix(labels) {
    var parts = [];
    for (var k in labels) {
      if (k !== "__name__" && Object.prototype.hasOwnProperty.call(labels, k)) {
        parts.push(escapeHTML(k) + "=" + escapeHTML(labels[k]));
      }
    }
    return parts.length ? " {" + parts.join(",") + "}" : "";
  }

  function refreshPanels() {
    loadAlerts();
    loadAnomalies();
  }

  function scheduleRefresh() {
    if (refreshTimer) {
      clearInterval(refreshTimer);
      refreshTimer = null;
    }
    if (autoRefresh.checked) {
      refreshTimer = setInterval(function () {
        loadAndRender();
        refreshPanels();
      }, 15000);
    }
  }

  metricSelect.addEventListener("change", onSelectionChange);
  rangeSelect.addEventListener("change", onSelectionChange);
  autoRefresh.addEventListener("change", scheduleRefresh);
  window.addEventListener("resize", loadAndRender);

  loadMetrics();
  refreshPanels();
  scheduleRefresh();
})();
