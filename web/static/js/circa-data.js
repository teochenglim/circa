// CircaData: fetch + math helpers shared by overview.js (the "Overall" tab's
// compact grid) and detail.js (the per-category tabs' bigger charts) — one
// implementation instead of duplicating query/rate/merge logic per script,
// the same reasoning as app.js's CircaChart namespace.
window.CircaData = (function () {
  "use strict";

  var WINDOW_SECONDS = 600; // last 10 minutes — matches the anomalies panel's own window

  function fetchJSON(url) {
    return fetch(url).then(function (r) {
      return r.json();
    });
  }

  // queryRange fetches one metric (optionally label-filtered) over the
  // fixed recent window and returns just the series array — every chart
  // built from this reads raw (tier=raw) points and does its own math
  // (rate, used-percent, ...) client-side, since /api/v1/query_range has
  // no PromQL-style functions server-side (DESIGN/05 §5).
  function queryRange(metric, labels) {
    var end = Math.floor(Date.now() / 1000);
    var start = end - WINDOW_SECONDS;
    var url = "/api/v1/query_range?metric=" + encodeURIComponent(metric) + "&start=" + start + "&end=" + end + "&tier=raw";
    if (labels) url += "&labels=" + encodeURIComponent(labels);
    return fetchJSON(url).then(function (body) {
      return body.status === "success" ? body.data.result || [] : [];
    });
  }

  // rateOf converts a counter series' raw [ts, value] points into a
  // per-second rate between consecutive points — the same math a PromQL
  // rate() would do. A negative delta (counter reset — process restart) is
  // dropped rather than plotted as a bogus negative spike.
  function rateOf(points) {
    var out = [];
    for (var i = 1; i < points.length; i++) {
      var dt = points[i][0] - points[i - 1][0];
      if (dt <= 0) continue;
      var dv = parseFloat(points[i][1]) - parseFloat(points[i - 1][1]);
      if (dv < 0) continue;
      out.push([points[i][0], dv / dt]);
    }
    return out;
  }

  // rateOfXY is rateOf, reshaped into the [xs, ys] pair-of-arrays format
  // mergeOnX/mergeAllOnX expect (rateOf itself returns an array of [ts, v]
  // pairs, the same shape raw .values comes in — a different shape, easy
  // to pass to the wrong function by mistake, hence the separate name).
  function rateOfXY(points) {
    var r = rateOf(points);
    return [r.map(function (p) { return p[0]; }), r.map(function (p) { return p[1]; })];
  }

  // sumRates sums rate() across every series in resultSeries onto a shared
  // timestamp axis — e.g. every disk device, every CPU.
  function sumRates(resultSeries) {
    var totals = {};
    resultSeries.forEach(function (s) {
      rateOf(s.values || []).forEach(function (pt) {
        totals[pt[0]] = (totals[pt[0]] || 0) + pt[1];
      });
    });
    var xs = Object.keys(totals).map(Number).sort(function (a, b) { return a - b; });
    return [xs, xs.map(function (x) { return totals[x]; })];
  }

  function pointsToXY(points) {
    return [points.map(function (p) { return p[0]; }), points.map(function (p) { return parseFloat(p[1]); })];
  }

  // mergeOnX widens an already-uPlot-shaped data array ([xs, y1, y2, ...])
  // with one more [xs, ys] series, null-filling gaps on the combined
  // x-axis. Re-aligns *every* existing y-series onto the new axis, not
  // just the incoming one — folding in a 3rd+ series without re-aligning
  // the earlier ones would silently desync their arrays from the new xs.
  function mergeOnX(data, extra) {
    var xSet = {};
    data[0].forEach(function (x) { xSet[x] = true; });
    extra[0].forEach(function (x) { xSet[x] = true; });
    var xs = Object.keys(xSet).map(Number).sort(function (a, b) { return a - b; });

    var out = [xs];
    for (var i = 1; i < data.length; i++) {
      var idx = {};
      data[0].forEach(function (x, j) { idx[x] = data[i][j]; });
      out.push(xs.map(function (x) { return x in idx ? idx[x] : null; }));
    }
    var extraIdx = {};
    extra[0].forEach(function (x, j) { extraIdx[x] = extra[1][j]; });
    out.push(xs.map(function (x) { return x in extraIdx ? extraIdx[x] : null; }));
    return out;
  }

  // mergeAllOnX folds a list of {xs, ys} series (already [xs,ys] pairs, as
  // produced by rateOf/pointsToXY per input series) into one uPlot-shaped
  // data array — used when a chart shows one line per label value (per
  // device, per cpu) rather than a fixed, known set of metrics.
  function mergeAllOnX(xyList) {
    if (xyList.length === 0) return [[]];
    var data = [xyList[0][0], xyList[0][1]];
    for (var i = 1; i < xyList.length; i++) {
      data = mergeOnX(data, xyList[i]);
    }
    return data;
  }

  // labelFor builds a short, readable legend label from a series' label
  // set, preferring a single distinguishing key (e.g. "device", "cpu",
  // "mountpoint") when the caller knows which one varies, falling back to
  // every non-__name__ label otherwise.
  function labelFor(metric, preferKey) {
    if (preferKey && metric[preferKey] !== undefined) return metric[preferKey];
    var parts = [];
    for (var k in metric) {
      if (k !== "__name__" && Object.prototype.hasOwnProperty.call(metric, k)) parts.push(k + "=" + metric[k]);
    }
    return parts.length ? parts.join(",") : (metric.__name__ || "value");
  }

  // categoryColor reads this category's assigned color straight from
  // app.css's --cat-* custom properties (see its "Per-category accent
  // utilities" comment) — one definition, so a chart's line, its Overall
  // card's left border, and its detail page's heading bar always agree
  // instead of drifting apart across separately-hardcoded hex codes.
  function categoryColor(category) {
    var v = getComputedStyle(document.documentElement).getPropertyValue("--cat-" + category);
    return v ? v.trim() : "#78716c";
  }

  // palette is for contexts a single category color can't cover — many
  // lines of the *same* category (one per CPU core, one per network
  // device) that still need to be visually distinguishable from each
  // other, not from other categories.
  var palette = ["#0ea5e9", "#f59e0b", "#8b5cf6", "#34d399", "#fb7185", "#eab308", "#14b8a6", "#a78bfa"];

  return {
    WINDOW_SECONDS: WINDOW_SECONDS,
    fetchJSON: fetchJSON,
    queryRange: queryRange,
    rateOf: rateOf,
    rateOfXY: rateOfXY,
    sumRates: sumRates,
    pointsToXY: pointsToXY,
    mergeOnX: mergeOnX,
    mergeAllOnX: mergeAllOnX,
    labelFor: labelFor,
    categoryColor: categoryColor,
    palette: palette,
  };
})();
