// Comments cluster sub-hook, extracted from useBeadsDetailPanel (mitto-90f.7
// PR-13). Owns the comments list state, the view-mode "add comment" editor
// state (draft/adding/saving flags + textarea ref), the focus effect, and
// the two comment handlers (startAddComment, handleCommentBlur).
//
// The composer's fetchDeps loop writes to setComments (three call sites), so
// setComments is exposed on the return bag. handleCommentBlur needs to call
// fetchDeps(false) after a successful POST — fetchDeps is defined later in
// the composer, so we reuse the fetchDepsRef bridge that PR-12 introduced for
// the labels cluster (composer sets fetchDepsRef.current = fetchDeps right
// after fetchDeps is defined). The composer's two issue-switch reset effects
// stay inline and call comments.setComments / comments.setAddingComment /
// comments.setSavingComment / comments.setCommentDraft.

const { useState, useEffect, useCallback, useRef } = window.preact;

import { getSdkClient } from "../../../utils/sdkClient.js";
import { errorMessage } from "../../../utils/sdkErrors.js";

export function useIssueComments({
  data,
  workingDir,
  showToast,
  fetchDepsRef,
  onUpdated,
}) {
  const [comments, setComments] = useState([]);

  // View-mode "add comment": a "+" button at the bottom of the comments list
  // reveals a textarea with the same save-on-blur behaviour as notes. An empty
  // draft on blur just closes the editor without a request; otherwise the
  // comment is posted via /api/issues/{id}/comments and the list is refreshed.
  const [addingComment, setAddingComment] = useState(false);
  const [commentDraft, setCommentDraft] = useState("");
  const [savingComment, setSavingComment] = useState(false);
  const commentRef = useRef(null);

  // Focus the new-comment textarea when the "add comment" editor opens.
  useEffect(() => {
    if (addingComment && commentRef.current) {
      commentRef.current.focus();
    }
  }, [addingComment]);

  // Open the new-comment editor with an empty draft.
  const startAddComment = useCallback(() => {
    if (savingComment) return;
    setCommentDraft("");
    setAddingComment(true);
  }, [savingComment]);

  // Persist a new comment on blur. An empty (whitespace-only) draft just closes
  // the editor without a request. On success the comment list is refreshed via
  // fetchDeps and the parent list is notified via onUpdated.
  const handleCommentBlur = useCallback(async () => {
    const text = commentDraft.trim();
    if (!text) {
      setAddingComment(false);
      return;
    }
    setSavingComment(true);
    try {
      await getSdkClient().issues.comments(
        data.id,
        { working_dir: workingDir },
        { text },
      );
      setCommentDraft("");
      showToast && showToast({ style: "success", title: "Comment added" });
      if (fetchDepsRef && fetchDepsRef.current) {
        await fetchDepsRef.current(false);
      }
      onUpdated && onUpdated();
    } catch (err) {
      showToast &&
        showToast({
          style: "error",
          title: errorMessage(err, "Failed to add comment"),
        });
    } finally {
      setSavingComment(false);
      setAddingComment(false);
    }
  }, [
    commentDraft,
    data && data.id,
    workingDir,
    showToast,
    fetchDepsRef,
    onUpdated,
  ]);

  return {
    comments,
    setComments,
    addingComment,
    setAddingComment,
    commentDraft,
    setCommentDraft,
    savingComment,
    setSavingComment,
    commentRef,
    startAddComment,
    handleCommentBlur,
  };
}
