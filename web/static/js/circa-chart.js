// CircaChart: shared uPlot legend behavior, loaded on every page that
// renders a chart (app.js's Metrics page, overview.js's Overall page,
// detail.js's per-category pages, v0.6.0) — one implementation instead of
// duplicating it per script.
//
// legend.isolate: true flips uPlot's own built-in click semantics (see its
// legend row click handler): a plain click on a legend entry hides every
// *other* series (Grafana's "solo" behavior), and clicking that same entry
// again — or any entry, once everything else is already hidden — shows
// every series again. That part is entirely uPlot's existing behavior,
// just switched on; nothing custom needed for it.
//
// What uPlot does *not* provide is "click empty chart area to restore all
// series" (Grafana has this too, as a second way back to the unfiltered
// view besides re-clicking the isolated legend entry) — bindShowAll adds
// that by listening on `.over` (uPlot's own interaction-layer div, exposed
// as `plot.over`). A real drag-to-zoom's resulting click is already
// suppressed by uPlot itself (its drag handler calls
// stopImmediatePropagation), so this only fires on a genuine plain click.
window.CircaChart = {
  legendOpts: function (extra) {
    return Object.assign({ isolate: true }, extra || {});
  },
  bindShowAll: function (plot) {
    if (!plot || !plot.over) return;
    plot.over.addEventListener("click", function () {
      plot.series.forEach(function (s, i) {
        if (i > 0) plot.setSeries(i, { show: true });
      });
    });
  },
};
