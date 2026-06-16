// Preact wrapper around CodeEditor (CodeMirror 6) for use as a form field.
// Exposes getValue/setValue/focus via an `editorApiRef` prop so parents can
// read/write content imperatively (e.g. for magic-wand AI improvement).

const { html, useRef, useEffect } = window.preact;

import { CodeEditor } from "../utils/code-editor.js";

/**
 * CodeEditorField — CodeMirror-backed editable field for Preact/HTM forms.
 *
 * @param {Object}   props
 * @param {string}   props.value        - Initial content (applied on mount only)
 * @param {Function} [props.onChange]   - Called on every content change: (text) => void
 * @param {Function} [props.onBlur]     - Called when editor loses focus: (text) => void
 * @param {boolean}  [props.disabled]   - When true, editor is read-only
 * @param {boolean}  [props.darkMode]   - Use dark theme
 * @param {number}   [props.minHeight]  - Minimum height in pixels for the container
 * @param {boolean}  [props.autoFocus]  - Focus the editor after init
 * @param {boolean}  [props.lineNumbers=true] - Show the line-number gutter
 * @param {boolean}  [props.lineWrapping=false] - Wrap long lines instead of scrolling horizontally
 * @param {boolean}  [props.highlightActiveLine=true] - Tint the current line's background
 * @param {string}   [props.className]  - Extra classes appended to the editor container
 * @param {Object}   [props.editorApiRef] - Assigned { getValue, setValue, focus } after init
 */
export function CodeEditorField({ value, onChange, onBlur, disabled, darkMode, minHeight, autoFocus, lineNumbers, lineWrapping, highlightActiveLine, className, editorApiRef }) {
  const containerRef = useRef(null);
  const editorRef = useRef(null);
  const destroyedRef = useRef(false);

  useEffect(() => {
    destroyedRef.current = false;
    const editor = new CodeEditor(containerRef.current, {
      readOnly: !!disabled,
      darkMode: !!darkMode,
      language: "md",
      lineNumbers: lineNumbers ?? true,
      lineWrapping: lineWrapping ?? false,
      highlightActiveLine: highlightActiveLine ?? true,
      onChange,
      onBlur,
    });
    editor.init(value ?? "").then(() => {
      if (destroyedRef.current) {
        editor.destroy();
        return;
      }
      editorRef.current = editor;
      if (editorApiRef) {
        editorApiRef.current = {
          getValue: () => editor.getValue(),
          setValue: (text) => editor.setValue(text),
          focus: () => editor.focus(),
        };
      }
      if (autoFocus) editor.focus();
    });
    return () => {
      destroyedRef.current = true;
      if (editorRef.current) {
        editorRef.current.destroy();
        editorRef.current = null;
      }
      if (editorApiRef) editorApiRef.current = null;
    };
  }, []); // mount/unmount only — deps intentionally omitted

  // Sync read-only state when `disabled` prop changes after mount.
  useEffect(() => {
    if (editorRef.current) editorRef.current.setReadOnly(!!disabled);
  }, [disabled]);

  const containerStyle = [
    "w-full min-w-0 max-w-full border border-mitto-border rounded bg-mitto-input-box text-sm text-mitto-text overflow-auto",
    "focus-within:border-mitto-text-secondary transition-colors",
    className || "",
  ].join(" ").trim();

  const style = minHeight ? `min-height:${minHeight}px` : undefined;

  return html`<div ref=${containerRef} class=${containerStyle} style=${style} />`;
}
