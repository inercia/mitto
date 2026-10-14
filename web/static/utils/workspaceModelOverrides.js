/** Return the overrides belonging to one exact ACP server name. */
export function modelOverridesForAgent(overrides = [], acpServer) {
  return (Array.isArray(overrides) ? overrides : []).filter(
    (override) => override?.acp_server === acpServer,
  );
}

/**
 * Replace one agent's ordered rows without disturbing any other agent's data.
 * The existing agent rows are replaced at their first position in the overall
 * list, keeping the relative order of other agents stable.
 */
export function replaceAgentModelOverrides(
  overrides = [],
  acpServer,
  rows = [],
) {
  const all = Array.isArray(overrides) ? overrides : [];
  const nextRows = (Array.isArray(rows) ? rows : []).map((row) => ({
    ...row,
    acp_server: acpServer,
  }));
  const firstIndex = all.findIndex(
    (override) => override?.acp_server === acpServer,
  );
  if (firstIndex < 0) return [...all, ...nextRows];

  const result = [];
  all.forEach((override, index) => {
    if (override?.acp_server !== acpServer) {
      result.push(override);
    } else if (index === firstIndex) {
      result.push(...nextRows);
    }
  });
  return result;
}

/** Include a saved model absent from the last-seen catalog as an option. */
export function modelCatalogOptions(models = [], selectedModelID = "") {
  const options = Array.isArray(models)
    ? models.filter((model) => model && typeof model.id === "string")
    : [];
  if (
    selectedModelID &&
    !options.some((model) => model.id === selectedModelID)
  ) {
    return [
      ...options,
      {
        id: selectedModelID,
        name: `${selectedModelID} (unavailable)`,
        unavailable: true,
      },
    ];
  }
  return options;
}
