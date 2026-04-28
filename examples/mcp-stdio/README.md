# MCP Stdio Example

This pod wraps `echo-server/server.js`, a tiny stdio MCP server, with the shared
`claw-mcp-stdio` sidecar.

```bash
claw discover
claw up -d
```

`claw discover` asks the MCP server for `tools/list` and writes the checked-in
`.claw-discovered/echo.claw-describe.json` snapshot. `claw up` then compiles the
managed `echo.echo` tool from that snapshot.
