/** Return the overrides belonging to one exact ACP server name. */
export function modelOverridesForAgent(overrides = [], acpServer) {
  return (Array.isArray(overrides) ? overrides : []).filter(
    (override) => override?.acp_server === acpServer,
  );
}

/** Group complete mappings by exact persisted model ID for editor drafts. */
export function groupModelOverrides(rows = []) {
  const groups = [];
  const byModel = new Map();

  (Array.isArray(rows) ? rows : []).forEach((row, index) => {
    const modelID = typeof row?.model_id === "string" ? row.model_id : "";
    const tag = typeof row?.tag === "string" ? row.tag : "";
    // Incomplete rows stay independent drafts rather than merging with saved
    // rows or with one another.
    const group = modelID && tag
        ? byModel.get(modelID) || {
          key: `model-${groups.length}-${modelID}`,
          model_id: modelID,
          order: index,
          tagCounts: [],
        }
      : {
          key: `incomplete-${index}`,
          model_id: modelID,
          order: index,
          tagCounts: [],
        };

    if (modelID && tag && !byModel.has(modelID)) {
      byModel.set(modelID, group);
      groups.push(group);
    } else if (!modelID || !tag) {
      groups.push(group);
    }

    const tagEntry = group.tagCounts.find((entry) => entry.tag === tag);
    if (tagEntry) tagEntry.orders.push(index);
    else group.tagCounts.push({ tag, orders: [index] });
  });

  return groups;
}

/** Expand grouped drafts back to the unchanged one-row-per-tag API shape. */
export function expandModelOverrideGroups(groups = []) {
  return (Array.isArray(groups) ? groups : [])
    .flatMap((group) => {
    const entries = Array.isArray(group?.tagCounts) ? group.tagCounts : [];
    if (entries.length === 0) {
      return [{
        tag: "",
        model_id: group?.model_id || "",
        order: group?.order ?? Number.MAX_SAFE_INTEGER,
      }];
    }
    return entries.flatMap(({ tag, orders = [] }) =>
      (orders.length ? orders : [group?.order ?? Number.MAX_SAFE_INTEGER]).map((order) => ({
        tag,
        model_id: group?.model_id || "",
        order,
      })),
    );
    })
    .sort((left, right) => left.order - right.order)
    .map(({ tag, model_id }) => ({ tag, model_id }));
}

/** Return tag conflicts that still have more than one model-group owner. */
export function modelOverrideTagConflicts(groups = []) {
  const owners = new Map();
  for (const group of Array.isArray(groups) ? groups : []) {
    for (const { tag } of group?.tagCounts || []) {
      if (!tag) continue;
      const models = owners.get(tag) || new Set();
      models.add(group.model_id || "");
      owners.set(tag, models);
    }
  }
  return [...owners.entries()]
    .filter(([, models]) => models.size > 1)
    .map(([tag, models]) => ({ tag, modelIDs: [...models] }));
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
