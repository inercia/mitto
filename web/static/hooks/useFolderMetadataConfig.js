// Mitto Web Interface — Folder Metadata Config Hook
//
// Owns the workspace-metadata state block for the folder editor: description,
// url, group, and the user-data-schema fields (loaded from .mittorc via the
// per-workspace metadata endpoint). Encapsulates the folder-selection effect
// that resets and reloads these fields from the first workspace in the group,
// plus the two persist actions (metadata PUT and user-data-schema PUT) that
// handleSave invokes after the config save. Extracted verbatim from
// WorkspacesDialog.js (mitto-90f.4 Increment D-5) so the shell can drop ~130
// LOC and pass two grouped objects (metadata / metadataSetters) instead of 8
// individual props through WorkspaceFolderEditor.
//
// Behavior-preserving: state names, setter names, effect deps, fetch shape and
// error surfacing all match the original shell code exactly. Both persist
// actions throw on failure so the shell's handleSave can catch and setError
// with the same "Failed to save metadata: " / "Failed to save user data
// schema: " prefixes it used before the extraction.
//
// The effect's dependency list stays `[selectedFolder]` — groupedWorkspaces is
// read via ref so folder switches trigger a reload but incidental array-ref
// churn does NOT (matches the pre-extraction behavior exactly).
//
// The hook takes { selectedFolder, groupedWorkspaces } and returns
// { metadata, metadataSetters, persistMetadata, persistUserDataSchema }.

const { useState, useEffect, useRef } = window.preact;

import { getSdkClient } from "../utils/sdkClient.js";
import { errorMessage } from "../utils/sdkErrors.js";

/**
 * useFolderMetadataConfig — cohesive state/handler bundle for the folder
 * Metadata + User Data Schema panels.
 *
 * @param {Object} params
 * @param {string|null} params.selectedFolder — the currently selected folder display name
 * @param {Array<{displayName: string, workspaces: Array<{uuid: string}>}>} params.groupedWorkspaces
 * @returns {{
 *   metadata: Object,
 *   metadataSetters: Object,
 *   persistMetadata: () => Promise<void>,
 *   persistUserDataSchema: () => Promise<void>,
 * }}
 */
export function useFolderMetadataConfig({ selectedFolder, groupedWorkspaces }) {
  // Workspace metadata loaded from .mittorc (description, url, group).
  const [folderMetadata, setFolderMetadata] = useState(null);
  const [metadataLoading, setMetadataLoading] = useState(false);
  const [editMetaDescription, setEditMetaDescription] = useState("");
  const [editMetaUrl, setEditMetaUrl] = useState("");
  const [editMetaGroup, setEditMetaGroup] = useState("");
  const [editUserDataFields, setEditUserDataFields] = useState([]);

  // Keep the latest groupedWorkspaces in a ref so persist* handlers can resolve
  // the folder's uuid without capturing a stale value, and so the load effect
  // can stay on its historical [selectedFolder] deps (matches the pre-D-5
  // shell behavior exactly).
  const groupedWorkspacesRef = useRef(groupedWorkspaces);
  useEffect(() => {
    groupedWorkspacesRef.current = groupedWorkspaces;
  }, [groupedWorkspaces]);

  const getFolderUuid = () => {
    const folderGroup = groupedWorkspacesRef.current.find(
      (g) => g.displayName === selectedFolder,
    );
    return folderGroup?.workspaces[0]?.uuid || null;
  };

  // When a folder is selected, reset metadata state and fetch it from the
  // first workspace's metadata endpoint. Matches the original effect body
  // that was tail-spliced onto the folder-selection effect in the shell.
  useEffect(() => {
    if (!selectedFolder) return;
    const folderGroup = groupedWorkspacesRef.current.find(
      (g) => g.displayName === selectedFolder,
    );
    const firstWs = folderGroup?.workspaces[0];
    if (!firstWs) return;

    // Load workspace metadata from .mittorc
    setFolderMetadata(null);
    setEditMetaDescription("");
    setEditMetaUrl("");
    setEditMetaGroup("");
    setEditUserDataFields([]);
    if (firstWs.uuid) {
      setMetadataLoading(true);
      getSdkClient()
        .workspaces.getMetadata(firstWs.uuid)
        .then((data) => {
          setFolderMetadata(data || null);
          setEditMetaDescription(data?.description || "");
          setEditMetaUrl(data?.url || "");
          setEditMetaGroup(data?.group || "");
          setEditUserDataFields(
            (data?.user_data_schema?.fields || []).map((f) => ({
              name: f.name || "",
              type: f.type || "string",
              description: f.description || "",
            })),
          );
        })
        .catch(() => {
          setFolderMetadata(null);
          setEditMetaDescription("");
          setEditMetaUrl("");
          setEditMetaGroup("");
          setEditUserDataFields([]);
        })
        .finally(() => {
          setMetadataLoading(false);
        });
    }
  }, [selectedFolder]);

  // Persist description/url/group via PUT /api/workspaces/:uuid/metadata.
  // Skipped when no folder is selected, no metadata field is populated, or
  // the folder has no resolvable workspace uuid. Throws on !res.ok so the
  // caller (handleSave) can wrap in try/catch and setError uniformly.
  const persistMetadata = async () => {
    if (!selectedFolder) return;
    if (!editMetaDescription && !editMetaUrl && !editMetaGroup) return;
    const folderWsUuid = getFolderUuid();
    if (!folderWsUuid) return;
    try {
      await getSdkClient().workspaces.setMetadata(folderWsUuid, {
        description: editMetaDescription,
        url: editMetaUrl,
        group: editMetaGroup,
      });
    } catch (err) {
      throw new Error(errorMessage(err, "Failed to save workspace metadata"));
    }
  };

  // Persist the user-data-schema field list via PUT
  // /api/workspaces/:uuid/user-data-schema. Filters out fields with empty
  // names (mirrors the shell's pre-extraction behavior). Skipped when no
  // folder is selected or the folder has no resolvable workspace uuid.
  const persistUserDataSchema = async () => {
    if (!selectedFolder) return;
    const folderWsUuid = getFolderUuid();
    if (!folderWsUuid) return;
    const validFields = editUserDataFields.filter((f) => f.name.trim() !== "");
    try {
      await getSdkClient().workspaces.setUserDataSchema(folderWsUuid, {
        fields: validFields,
      });
    } catch (err) {
      throw new Error(errorMessage(err, "Failed to save user data schema"));
    }
  };

  const metadata = {
    folderMetadata,
    metadataLoading,
    editMetaDescription,
    editMetaUrl,
    editMetaGroup,
    editUserDataFields,
  };
  const metadataSetters = {
    setEditMetaDescription,
    setEditMetaUrl,
    setEditMetaGroup,
    setEditUserDataFields,
  };
  return { metadata, metadataSetters, persistMetadata, persistUserDataSchema };
}
