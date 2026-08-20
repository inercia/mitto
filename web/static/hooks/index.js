// Mitto Web Interface - Hooks Index
// Re-exports all custom hooks for convenient importing

export { useWebSocket } from "./useWebSocket.js";
export { useSwipeNavigation } from "./useSwipeNavigation.js";
export { useResizeHandle } from "./useResizeHandle.js";
export { useSwipeToAction } from "./useSwipeToDelete.js";
export { useInfiniteScroll } from "./useInfiniteScroll.js";
export { useToast } from "./useToast.js";
export { useTheme } from "./useTheme.js";
export { useBackgroundNotifications } from "./useBackgroundNotifications.js";
export { useScrollManagement } from "./useScrollManagement.js";
export { useQueueActions } from "./useQueueActions.js";
export { useAgentPlan } from "./useAgentPlan.js";
export { useWorkspacePrompts } from "./useWorkspacePrompts.js";
export {
  useBeadsIntegration,
  buildBeadsPromptToast,
} from "./useBeadsIntegration.js";
export { useSessionNavigation } from "./useSessionNavigation.js";
export { useConversationMenu } from "./useConversationMenu.js";
export {
  buildSeedQueueBody,
  seedConversationWithPrompt,
  decideLoopAction,
  makeLoopNow,
  useConversationSeeding,
} from "./useConversationSeeding.js";
export { useBeadsKnownIds } from "./useBeadsKnownIds.js";
export { useLinkedBeadPhase } from "./useLinkedBeadPhase.js";
export { useVisibleInterval } from "./useVisibleInterval.js";
export { useMCPInitState } from "./useMCPInitState.js";
