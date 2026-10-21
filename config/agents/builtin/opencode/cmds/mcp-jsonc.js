#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import { parse, parseTree, modify, applyEdits } from 'jsonc-parser/lib/esm/main.js';

const action = process.argv[2];
const inputText = fs.readFileSync(0, 'utf8');

function parseConfig(text, configPath) {
  const errors = [];
  const config = parse(text, errors, { allowTrailingComma: true, disallowComments: false });
  if (errors.length) {
    throw new Error(`${configPath}: invalid JSONC (parse error ${errors[0].error} at offset ${errors[0].offset})`);
  }
  if (!config || typeof config !== 'object' || Array.isArray(config)) {
    throw new Error(`${configPath}: OpenCode config must contain a JSON object`);
  }
  return config;
}

function findExisting(home, workspace) {
  const selected = [];
  const scopes = [];
  if (home) scopes.push([path.join(home, '.config', 'opencode', 'opencode.jsonc'), path.join(home, '.config', 'opencode', 'opencode.json')]);
  if (workspace) scopes.push([path.join(workspace, 'opencode.jsonc'), path.join(workspace, 'opencode.json')]);
  for (const variants of scopes) {
    const found = variants.find(file => fs.existsSync(file));
    if (found) selected.push(found);
  }
  return selected;
}

function asServer(name, cfg) {
  if (!cfg || typeof cfg !== 'object' || Array.isArray(cfg)) return null;
  const entry = { name };
  const kind = cfg.type;
  const command = cfg.command;
  if (kind === 'local' || (kind == null && (Array.isArray(command) || typeof command === 'string'))) {
    if (Array.isArray(command) && command.length) {
      entry.command = command[0];
      if (command.length > 1) entry.args = command.slice(1);
    } else if (typeof command === 'string' && command) entry.command = command;
    if (cfg.environment) entry.env = cfg.environment;
  }
  if (kind === 'remote' || (kind == null && Object.hasOwn(cfg, 'url'))) {
    if (Object.hasOwn(cfg, 'url')) entry.url = cfg.url;
    if (Object.hasOwn(cfg, 'headers')) entry.headers = cfg.headers;
  }
  return entry;
}

function list() {
  const input = inputText.trim() ? JSON.parse(inputText) : {};
  const merged = {};
  // Global config is applied first; workspace config overrides duplicate names.
  for (const configPath of findExisting(process.env.HOME, input.path)) {
    let config;
    try {
      config = parseConfig(fs.readFileSync(configPath, 'utf8'), configPath);
    } catch (error) {
      throw error;
    }
    if (config.mcp != null && (typeof config.mcp !== 'object' || Array.isArray(config.mcp))) {
      throw new Error(`${configPath}: OpenCode config mcp property must be an object`);
    }
    Object.assign(merged, config.mcp || {});
  }
  return { servers: Object.entries(merged).map(([name, cfg]) => asServer(name, cfg)).filter(Boolean) };
}

function targetConfig(input) {
  const home = process.env.HOME;
  if (input.scope === 'project') {
    if (!input.path) throw new Error('path is required for project scope');
    const existing = findExisting('', input.path)[0];
    return existing || path.join(input.path, 'opencode.json');
  }
  const existing = findExisting(home, '')[0];
  return existing || path.join(home, '.config', 'opencode', 'opencode.json');
}

function commaBetween(text, start, end) {
  let inString = false;
  let escaped = false;
  for (let i = start; i < end; i++) {
    const ch = text[i];
    if (inString) {
      if (escaped) escaped = false;
      else if (ch === '\\') escaped = true;
      else if (ch === '"') inString = false;
    } else if (ch === '"') inString = true;
    else if (text.startsWith('//', i)) {
      const newline = text.indexOf('\n', i + 2);
      i = newline < 0 || newline >= end ? end : newline;
    } else if (text.startsWith('/*', i)) {
      const close = text.indexOf('*/', i + 2);
      i = close < 0 || close >= end ? end : close + 1;
    } else if (ch === ',') return i;
  }
  return -1;
}

function removeServer(text, name) {
  const errors = [];
  const root = parseTree(text, errors, { allowTrailingComma: true, disallowComments: false });
  if (errors.length || !root) throw new Error(`invalid JSONC (parse error ${errors[0]?.error ?? 'unknown'})`);
  const property = (object, key) => object?.children?.find(child => child.type === 'property' && child.children?.[0]?.value === key);
  const mcpNode = property(root, 'mcp')?.children?.[1];
  const objectProperties = mcpNode?.children || [];
  const index = objectProperties.findIndex(child => child.children?.[0]?.value === name);
  if (index < 0) throw new Error(`MCP server ${JSON.stringify(name)} not found`);

  const node = objectProperties[index];
  const previous = objectProperties[index - 1];
  const next = objectProperties[index + 1];
  const edits = [{ offset: node.offset, length: node.length, content: '' }];
  if (next) {
    const comma = commaBetween(text, node.offset + node.length, next.offset);
    if (comma >= 0) edits.push({ offset: comma, length: 1, content: '' });
  } else {
    if (previous) {
      const comma = commaBetween(text, previous.offset + previous.length, node.offset);
      if (comma >= 0) edits.push({ offset: comma, length: 1, content: '' });
    }
    const closingBrace = mcpNode.offset + mcpNode.length - 1;
    const trailingComma = commaBetween(text, node.offset + node.length, closingBrace);
    if (trailingComma >= 0) edits.push({ offset: trailingComma, length: 1, content: '' });
  }
  return applyEdits(text, edits);
}

function writeAction(remove) {
  const input = JSON.parse(inputText);
  const name = input.name || '';
  if (!name) throw new Error('name is required');
  const configPath = targetConfig(input);
  let text;
  let config;
  try {
    text = fs.readFileSync(configPath, 'utf8');
    config = parseConfig(text, configPath);
  } catch (error) {
    if (error.code !== 'ENOENT') throw error;
    text = '{}\n';
    config = {};
    fs.mkdirSync(path.dirname(configPath), { recursive: true });
  }
  const servers = config.mcp || {};
  if (config.mcp != null && (typeof config.mcp !== 'object' || Array.isArray(config.mcp))) {
    throw new Error(`${configPath}: OpenCode config mcp property must be an object`);
  }
  if (remove && !Object.hasOwn(servers, name)) throw new Error(`MCP server ${JSON.stringify(name)} not found`);
  if (!remove && !input.command && !input.url) throw new Error('either command or url is required');

  let output;
  if (remove) output = removeServer(text, name);
  else {
    const entry = input.url
      ? { type: 'remote', url: input.url, ...(input.headers ? { headers: input.headers } : {}) }
      : { type: 'local', command: [input.command, ...(input.args || [])], ...(input.env ? { environment: input.env } : {}) };
    const edits = modify(text, ['mcp', name], entry, { formattingOptions: { insertSpaces: true, tabSize: 2 } });
    output = applyEdits(text, edits);
  }
  fs.writeFileSync(configPath, output, 'utf8');
  return { success: true, message: `${remove ? 'Removed' : 'Added'} MCP server ${JSON.stringify(name)}`, name };
}

try {
  const result = action === 'list' ? list() : action === 'install' ? writeAction(false) : action === 'remove' ? writeAction(true) : (() => { throw new Error(`unknown action: ${action}`); })();
  process.stdout.write(`${JSON.stringify(result)}\n`);
} catch (error) {
  if (action === 'list') {
    process.stderr.write(`${error.message}\n`);
    process.exitCode = 1;
  } else {
    const name = (() => { try { return JSON.parse(inputText).name || ''; } catch { return ''; } })();
    process.stdout.write(`${JSON.stringify({ success: false, message: error.message, name })}\n`);
    process.exitCode = 1;
  }
}
