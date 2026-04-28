#!/usr/bin/env node

const readline = require("node:readline");

const rl = readline.createInterface({
  input: process.stdin,
  crlfDelay: Infinity,
});

function send(message) {
  process.stdout.write(`${JSON.stringify(message)}\n`);
}

rl.on("line", (line) => {
  if (!line.trim()) return;
  let req;
  try {
    req = JSON.parse(line);
  } catch {
    return;
  }
  if (req.id === undefined || req.id === null) return;

  if (req.method === "initialize") {
    send({
      jsonrpc: "2.0",
      id: req.id,
      result: {
        protocolVersion: req.params?.protocolVersion || "2025-11-25",
        serverInfo: { name: "mcp-echo-stdio", version: "0.1.0" },
        capabilities: { tools: {} },
      },
    });
    return;
  }

  if (req.method === "tools/list") {
    send({
      jsonrpc: "2.0",
      id: req.id,
      result: {
        tools: [
          {
            name: "echo",
            description: "Echo a message back as MCP text content.",
            inputSchema: {
              type: "object",
              properties: { message: { type: "string" } },
              required: ["message"],
            },
            annotations: { readOnly: true },
          },
        ],
      },
    });
    return;
  }

  if (req.method === "tools/call" && req.params?.name === "echo") {
    send({
      jsonrpc: "2.0",
      id: req.id,
      result: {
        content: [{ type: "text", text: String(req.params?.arguments?.message || "") }],
      },
    });
    return;
  }

  send({
    jsonrpc: "2.0",
    id: req.id,
    error: { code: -32601, message: `unknown method ${req.method}` },
  });
});
