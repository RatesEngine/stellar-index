// Build-time CHANGELOG.md parser shared by /changelog (rendered
// page) and /changelog.atom (syndication feed). Reads the file
// from repo root (../../CHANGELOG.md relative to web/explorer)
// and returns one record per release.

import { readFileSync } from 'node:fs';
import { join } from 'node:path';

export interface Release {
  version: string;
  date?: string;
  blocks: { kind: string; lines: string[] }[];
  raw: string;
}

export function loadReleases(): Release[] {
  let text = '';
  try {
    text = readFileSync(join(process.cwd(), '../../CHANGELOG.md'), 'utf8');
  } catch {
    return [];
  }
  const lines = text.split('\n');
  const releases: Release[] = [];
  let cur: Release | null = null;
  let curBlock: { kind: string; lines: string[] } | null = null;

  for (const line of lines) {
    const releaseMatch = line.match(/^##\s+\[([^\]]+)\](?:\s+—\s+(\S+))?/);
    if (releaseMatch) {
      if (cur) {
        if (curBlock) cur.blocks.push(curBlock);
        releases.push(cur);
      }
      cur = {
        version: releaseMatch[1]!,
        date: releaseMatch[2],
        blocks: [],
        raw: line + '\n',
      };
      curBlock = null;
      continue;
    }
    const blockMatch = line.match(/^###\s+(.+)$/);
    if (blockMatch && cur) {
      if (curBlock) cur.blocks.push(curBlock);
      curBlock = { kind: blockMatch[1]!.trim(), lines: [] };
      cur.raw += line + '\n';
      continue;
    }
    if (cur) {
      cur.raw += line + '\n';
      if (curBlock) curBlock.lines.push(line);
    }
  }
  if (cur) {
    if (curBlock) cur.blocks.push(curBlock);
    releases.push(cur);
  }
  return releases.filter(
    (r) => r.version.toLowerCase() !== 'unreleased' || r.blocks.length > 0,
  );
}
