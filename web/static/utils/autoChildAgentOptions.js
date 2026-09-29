export function buildAutoChildAgentOptions(
  workspaces,
  currentWorkspaceUUID,
  currentTarget,
) {
  const list = workspaces || [];
  const currentWs = list.find((ws) => ws.uuid === currentWorkspaceUUID);
  const sameDir = list.filter(
    (ws) =>
      ws.uuid !== currentWorkspaceUUID &&
      currentWs &&
      ws.working_dir === currentWs.working_dir,
  );
  const currentAgent = currentWs?.acp_server;

  const options = [{ value: "", label: "Current agent", workspace: null }];
  const seenAgents = new Set();
  let hasAlternatives = false;

  for (const ws of sameDir) {
    const agent = ws.acp_server;
    if (agent && agent === currentAgent) continue;
    if (agent && seenAgents.has(agent)) continue;
    if (agent) seenAgents.add(agent);
    options.push({
      value: ws.uuid,
      label: ws.acp_server || ws.name || "",
      workspace: ws,
    });
    hasAlternatives = true;
  }

  if (currentTarget && !options.some((o) => o.value === currentTarget)) {
    const targetWs = list.find((ws) => ws.uuid === currentTarget);
    options.push({
      value: currentTarget,
      label: targetWs
        ? targetWs.acp_server || targetWs.name || ""
        : `Unknown agent (${currentTarget})`,
      workspace: targetWs || null,
    });
  }

  return { options, hasAlternatives };
}
