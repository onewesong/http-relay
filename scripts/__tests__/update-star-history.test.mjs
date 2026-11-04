import { test } from 'node:test';
import assert from 'node:assert/strict';
import { historyFromWeeklyBuckets, renderChart, curve, validateHistory } from '../update-star-history.mjs';

const week = (date, days) => ({
  week: Date.parse(`${date}T00:00:00Z`) / 1000,
  total: days.reduce((sum, n) => sum + n, 0),
  days,
});

test('weekly aggregate buckets become a continuous daily cumulative history', () => {
  const history = historyFromWeeklyBuckets([
    week('2026-01-11', [1, 0, 2, 0, 0, 0, 0]),
    week('2026-01-04', [0, 1, 2, 0, 0, 0, 0]),
  ], 5, '2026-11-04');

  assert.equal(history.trackingSince, '2026-01-04');
  assert.equal(history.points.length, 14);
  assert.deepEqual(history.points.slice(0, 4), [
    ['2026-01-04', 0], ['2026-01-05', 1], ['2026-01-06', 3], ['2026-01-07', 3],
  ]);
  assert.deepEqual(history.points.slice(-7), [
    ['2026-01-11', 4], ['2026-01-12', 4], ['2026-01-13', 6], ['2026-01-14', 6],
    ['2026-01-15', 6], ['2026-01-16', 6], ['2026-01-17', 6],
  ]);
  assert.equal(history.currentStarCount, 5);
  assert.equal(validateHistory(history), history);
});

test('current partial week is clipped to the generated date', () => {\n  const history = historyFromWeeklyBuckets([week('2026-01-04', [0, 1, 2, 3, 4, 5, 6])], 3, '2026-01-06');\n  assert.deepEqual(history.points, [['2026-01-04', 0], ['2026-01-05', 1], ['2026-01-06', 3]]);\n  assert.equal(history.points.at(-1)[0], history.generatedAt);\n});\n\ntest('invalid weekly data and invalid current counts are rejected', () => {
  assert.throws(() => historyFromWeeklyBuckets([], 0));
  assert.throws(() => historyFromWeeklyBuckets([week('2026-01-04', [1, 0, 0, 0, 0, 0, 0])], -1));
  assert.throws(() => historyFromWeeklyBuckets([{ ...week('2026-01-04', [1, 0, 0, 0, 0, 0, 0]), total: 2 }], 1));
  assert.throws(() => historyFromWeeklyBuckets([week('2026-01-04', [1, 0, 0, 0, 0, 0, 0]), week('2026-01-04', [1, 0, 0, 0, 0, 0, 0])], 2));
});

test('rendered chart distinguishes cumulative additions from current active stars', () => {
  const history = historyFromWeeklyBuckets([week('2026-01-04', [1, 0, 0, 0, 0, 0, 0])], 0);
  const svg = renderChart(history);
  assert.match(svg, /1 cumulative stars added/);
  assert.match(svg, /Current active stars: 0/);
  assert.doesNotMatch(svg, /NaN|Infinity/);
});

test('rounded curve segments stay between observations across rises, plateaus and declines', () => {
  const points = [[0, 0], [1, 4], [2, 4], [3, 1], [4, 8]];
  const segments = curve(points).split(' C').slice(1);
  segments.forEach((segment, i) => {
    const [a, b, c] = segment.split(' ').map(pair => pair.split(',').map(Number));
    const y0 = points[i][1], y1 = points[i + 1][1];
    for (let j = 0; j <= 20; j++) {
      const t = j / 20, u = 1 - t;
      const value = u ** 3 * y0 + 3 * u ** 2 * t * a[1] + 3 * u * t ** 2 * b[1] + t ** 3 * c[1];
      assert.ok(value >= Math.min(y0, y1) - 0.001 && value <= Math.max(y0, y1) + 0.001);
    }
  });
});
