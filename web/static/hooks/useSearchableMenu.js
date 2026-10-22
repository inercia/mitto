// Opt-in navigation for an already ordered, filtered list of selectable items.
const { useState, useRef, useEffect, useCallback } = window.preact;

export function useSearchableMenu({ items, onAccept, onClose }) {
  const [selectedIndex, setSelectedIndex] = useState(-1);
  const selectedItemRef = useRef(null);

  // Preserve index-driven scrolling; replacing items alone must not scroll.
  useEffect(() => {
    if (selectedIndex >= 0) {
      selectedItemRef.current?.scrollIntoView({ block: "nearest" });
    }
  }, [selectedIndex]);

  const resetSelection = useCallback(() => setSelectedIndex(-1), []);
  const highlightIndex = useCallback((index) => setSelectedIndex(index), []);

  const handleKeyDown = (e) => {
    // Prevent the event from bubbling to the textarea, including unknown keys.
    e.stopPropagation();
    if (e.key === "Escape") {
      e.preventDefault();
      onClose();
      return;
    }
    if (e.key === "ArrowDown") {
      e.preventDefault();
      setSelectedIndex((prev) => Math.min(prev + 1, items.length - 1));
      return;
    }
    if (e.key === "ArrowUp") {
      e.preventDefault();
      setSelectedIndex((prev) => Math.max(-1, prev - 1));
      return;
    }
    if (e.key === "Enter") {
      e.preventDefault();
      if (selectedIndex >= 0 && items.length > 0) {
        const clampedIndex = Math.min(
          Math.max(selectedIndex, 0),
          items.length - 1,
        );
        onAccept(items[clampedIndex], e);
      }
      return;
    }
  };

  return {
    selectedIndex,
    selectedItemRef,
    handleKeyDown,
    resetSelection,
    highlightIndex,
  };
}
