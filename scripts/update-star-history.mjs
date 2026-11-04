#!/usr/bin/env node
// Rebuild privacy-safe star history from GitHub's aggregate weekly API.
// Historical points are cumulative stars added; removed stars are not subtracted.
import { execFileSync } from 'node:child_process';
import { mkdir, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const repository = 'onewesong/http-relay';
const branch = 'readme-star-history';
const apiVersion = '2026-03-10';

function api(path, body) {
  const args = ['api', `repos/${repository}${path ? `/${path}` : ''}`, '-H', `X-GitHub-Api-Version: ${apiVersion}`];
  if (body) args.push('--method', 'POST', '--input', '-');
  return JSON.parse(execFileSync('gh', args, {
    input: body ? JSON.stringify(body) : undefined, encoding: 'utf8',
    stdio: ['pipe', 'pipe', 'pipe'], timeout: 30_000,
  }));
}

function optionalApi(path) {
  try { return api(path); } catch (error) {
    if (String(error.stderr).includes('(HTTP 404)')) return null;
    throw error;
  }
}

export function validateHistory(history) {
  if (history?.repository !== repository ||
      history?.source !== 'github-stargazers-history-api' ||
      history?.metric !== 'cumulative-stars-added' ||
      !/^\d{4}-\d{2}-\d{2}$/.test(history.trackingSince) ||
      !Number.isSafeInteger(history.currentStarCount) || history.currentStarCount < 0 ||
      !Array.isArray(history.points) || !history.points.length ||
      history.points[0][0] !== history.trackingSince) {
    throw new Error('Invalid star history');
  }
  let previous = '';
  for (const point of history.points) {
    if (!Array.isArray(point) || point.length !== 2) throw new Error('Invalid star observation');
    const [date, count] = point;
    if (typeof date !== 'string' || !/^\d{4}-\d{2}-\d{2}$/.test(date) ||
        !Number.isFinite(Date.parse(date)) || new Date(date).toISOString().slice(0, 10) !== date ||
        date <= previous || !Number.isSafeInteger(count) || count < 0) {
      throw new Error('Invalid star observation');
    }
    previous = date;
  }
  if (history.points.some((point, i, points) => i > 0 && point[1] < points[i - 1][1])) {
    throw new Error('Cumulative star history cannot decrease');
  }
  return history;
}

// GitHub returns weekly additions, newest week first; days are Sunday through Saturday.
export function historyFromWeeklyBuckets(buckets, currentStarCount, generatedAt = new Date().toISOString().slice(0, 10)) {
  if (!Array.isArray(buckets) || !buckets.length) throw new Error('GitHub returned no star history');
  if (!/^\\d{4}-\\d{2}-\\d{2}$/.test(generatedAt) || new Date(generatedAt).toISOString().slice(0, 10) !== generatedAt) throw new Error('Invalid generated date');
  if (!Number.isSafeInteger(currentStarCount) || currentStarCount < 0) throw new Error('Invalid current star count');

  const weeks = [...buckets].sort((a, b) => a.week - b.week);
  const points = [];
  let cumulative = 0, previousWeek = -Infinity;
  for (const week of weeks) {
    if (!Number.isSafeInteger(week.week) || week.week <= previousWeek ||
        !Array.isArray(week.days) || week.days.length !== 7 ||
        week.days.some(n => !Number.isSafeInteger(n) || n < 0) ||
        week.total !== week.days.reduce((sum, n) => sum + n, 0)) {
      throw new Error('Invalid GitHub weekly star bucket');
    }
    previousWeek = week.week;
    for (let day = 0; day < 7; day++) {
      cumulative += week.days[day];
      const date = new Date((week.week + day * 86400) * 1000).toISOString().slice(0, 10);
      if (date > generatedAt) break;
      if (points.length && date <= points.at(-1)[0]) throw new Error('Overlapping GitHub star-history weeks');
      points.push([date, cumulative]);
    }
  }
  return validateHistory({
    repository,
    source: 'github-stargazers-history-api',
    metric: 'cumulative-stars-added',
    trackingSince: points[0][0],
    generatedAt,
    currentStarCount,
    points,
  });
}

export function curve(points) {
  if (points.length === 1) return `M${points[0].join(',')}`;
  const slopes = points.slice(1).map(([x, y], i) => (y - points[i][1]) / (x - points[i][0]));
  const tangents = points.map((_, i) => {
    if (!i) return slopes[0];
    if (i === points.length - 1) return slopes.at(-1);
    const a = slopes[i - 1], b = slopes[i];
    return a * b <= 0 ? 0 : Math.sign(a) * Math.min(Math.abs((a + b) / 2), 3 * Math.min(Math.abs(a), Math.abs(b)));
  });
  const f = n => Number(n.toFixed(2));
  return points.slice(1).reduce((path, [x, y], i) => {
    const [px, py] = points[i], dx = (x - px) / 3;
    return `${path} C${f(px + dx)},${f(py + tangents[i] * dx)} ${f(x - dx)},${f(y - tangents[i + 1] * dx)} ${f(x)},${f(y)}`;
  }, `M${points[0].map(f).join(',')}`);
}

export function renderChart(history, { dark = false, mobile = false } = {}) {
  validateHistory(history);
  const width = mobile ? 360 : 800, height = mobile ? 228 : 250;
  const left = mobile ? 30 : 44, right = width - (mobile ? 26 : 42), top = 42, bottom = height - 36;
  const ink = dark ? '#e6edf3' : '#24292f', muted = dark ? '#9198a1' : '#656d76', grid = dark ? '#30363d' : '#d8dee4';
  const data = history.points, start = Date.parse(data[0][0]), end = Date.parse(data.at(-1)[0]);
  const max = Math.max(1, ...data.map(([, count]) => count));
  const magnitude = 10 ** Math.floor(Math.log10(max));
  const step = magnitude * ([1, 2, 5, 10].find(n => n * magnitude >= max / 3) ?? 10);
  const ceiling = Math.ceil(max / step) * step;
  const x = date => left + (Date.parse(date) - start) / Math.max(86400000, end - start) * (right - left);
  const y = count => bottom - count / ceiling * (bottom - top);
  const points = data.map(([date, count]) => [x(date), y(count)]);
  const [lastX, lastY] = points.at(-1), last = data.at(-1);
  const tick = count => count >= 1000000 ? `${Number((count / 1000000).toFixed(1))}m` : count >= 1000 ? `${Number((count / 1000).toFixed(1))}k` : count;
  const lines = [];
  for (let count = 0; count <= ceiling; count += step) {
    lines.push(`<path d="M${left} ${y(count)}H${right}" stroke="${grid}" stroke-width="0.6"/><text x="${left - 10}" y="${y(count) + 4}" text-anchor="end">${tick(count)}</text>`);
  }
  const indices = [...new Set([0, Math.round((data.length - 1) / 3), Math.round((data.length - 1) * 2 / 3), data.length - 1])];
  for (const index of indices) {
    const date = data[index][0];
    lines.push(`<text x="${x(date)}" y="${height - 12}" text-anchor="middle">${date.slice(5).replace('-', '.')}</text>`);
  }
  return `<svg xmlns="http://www.w3.org/2000/svg" width="${width}" height="${height}" viewBox="0 0 ${width} ${height}" role="img" aria-labelledby="title desc">
  <title id="title">http-relay · ${last[1].toLocaleString('en-US')} cumulative stars added</title>
  <desc id="desc">${data[0][0]} to ${last[0]}. Cumulative stars added from GitHub's aggregate history API; removed stars are not subtracted. Current active stars: ${history.currentStarCount}.</desc>
  <g fill="${muted}" font-family="-apple-system,BlinkMacSystemFont,Segoe UI,Helvetica,Arial,sans-serif" font-size="12">
    ${lines.join('\n    ')}
    <path d="${curve(points)}" fill="none" stroke="${ink}" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"/>
    <path d="M0 -7 L2.06 -2.83 L6.66 -2.16 L3.33 1.08 L4.11 5.66 L0 3.5 L-4.11 5.66 L-3.33 1.08 L-6.66 -2.16 L-2.06 -2.83 Z" transform="translate(${lastX} ${lastY})" fill="${ink}"/>
    <text x="${lastX - 14}" y="${lastY - 13}" text-anchor="end" fill="${ink}" font-size="14" font-weight="600">${last[1].toLocaleString('en-US')}</text>
  </g>
</svg>\n`;
}

export async function generate(history, output) {
  validateHistory(history);
  const files = { 'history.json': `${JSON.stringify(history, null, 2)}\n` };
  for (const dark of [false, true]) for (const mobile of [false, true]) {
    files[`stars-${dark ? 'dark' : 'light'}${mobile ? '-mobile' : ''}.svg`] = renderChart(history, { dark, mobile });
  }
  await mkdir(output, { recursive: true });
  await Promise.all(Object.entries(files).map(([name, content]) => writeFile(resolve(output, name), content)));
  return files;
}

async function fetchAllWeeklyHistory() {
  const buckets = [];
  for (let page = 1; page <= 100; page++) {
    const result = api(`stargazers/history?per_page=30&page=${page}`);
    if (!Array.isArray(result)) throw new Error('Unexpected GitHub star-history response');
    if (!result.length) return buckets;
    buckets.push(...result);
  }
  throw new Error('GitHub star history exceeded the 100-page API limit');
}

async function main() {
  const args = process.argv.slice(2), publish = args.includes('--publish'), refresh = args.includes('--refresh');
  if (publish && !refresh) throw new Error('--publish requires --refresh');
  if (!refresh) throw new Error('Use --refresh to fetch the complete GitHub star history');
  const output = resolve(args.includes('--output') ? args[args.indexOf('--output') + 1] : '.github/assets/star-history');
  const repo = api('');
  const buckets = await fetchAllWeeklyHistory();
  const history = historyFromWeeklyBuckets(buckets, repo.stargazers_count);
  const files = await generate(history, output);
  if (publish) {
    // This dedicated branch contains only generated public assets. Never main.
    const head = optionalApi(`git/ref/heads/${branch}`)?.object.sha ?? null;
    const tree = api('git/trees', { tree: Object.entries(files).map(([path, content]) => ({ path, mode: '100644', type: 'blob', content })) });
    if (head && api(`git/commits/${head}`).tree.sha === tree.sha) {
      console.log('Star history unchanged'); return;
    }
    const commit = api('git/commits', {
      message: 'chore: rebuild README star history from GitHub aggregate API\n\nSigned-off-by: github-actions[bot] <41898282+github-actions[bot]@users.noreply.github.com>',
      tree: tree.sha, parents: head ? [head] : [],
      author: { name: 'github-actions[bot]', email: '41898282+github-actions[bot]@users.noreply.github.com' },
    });
    if (head) {
      execFileSync('gh', ['api', '--method', 'PATCH', `repos/${repository}/git/refs/heads/${branch}`, '--input', '-'], {
        input: JSON.stringify({ sha: commit.sha, force: false }), stdio: ['pipe', 'pipe', 'pipe'], timeout: 30000,
      });
    } else api('git/refs', { ref: `refs/heads/${branch}`, sha: commit.sha });
    console.log(`Published ${branch} at ${commit.sha}`);
  }
  console.log(`Generated ${history.points.length} daily points from ${history.trackingSince}; ${history.currentStarCount} active stars`);
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main().catch(error => { console.error(error.message); process.exitCode = 1; });
}
