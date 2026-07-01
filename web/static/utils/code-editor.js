/**
 * CodeEditor — Reusable CodeMirror 6 editor wrapper.
 *
 * Portable component for use in the file viewer, prompt editor, etc.
 * Supports read-only viewing with syntax highlighting and full editing.
 *
 * Usage:
 *   const editor = new CodeEditor(container, {
 *     readOnly: true,
 *     darkMode: true,
 *     fontSize: 13,
 *     language: "js",
 *     onChange: (content) => { ... },
 *   });
 *   await editor.init("file content here");
 *
 *   // Later: switch to edit mode
 *   editor.setReadOnly(false);
 *
 *   // Get content
 *   const text = editor.getValue();
 *
 *   // Clean up
 *   editor.destroy();
 */

import { loadCore, loadDarkTheme, loadLanguage } from "./editor-loader.js";

export class CodeEditor {
  /**
   * @param {HTMLElement} container - DOM element to mount the editor in
   * @param {Object} options
   * @param {boolean} [options.readOnly=true]   - Start in read-only mode
   * @param {boolean} [options.darkMode=true]   - Use dark theme
   * @param {number}  [options.fontSize=13]     - Font size in pixels
   * @param {string}  [options.language=null]   - File extension for syntax highlighting (e.g., "js", "py")
   * @param {boolean} [options.lineNumbers=true] - Show the line-number gutter (and fold/active-line gutter)
   * @param {boolean} [options.lineWrapping=false] - Wrap long lines instead of scrolling horizontally
   * @param {boolean} [options.highlightActiveLine=true] - Tint the current line's background
   * @param {Function} [options.onChange=null]  - Callback when content changes: (content: string) => void
   * @param {Function} [options.onBlur=null]    - Callback when the editor loses focus: (content: string) => void
   */
  constructor(container, options = {}) {
    this.container = container;
    this.readOnly = options.readOnly ?? true;
    this.darkMode = options.darkMode ?? true;
    this.fontSize = options.fontSize ?? 13;
    this.language = options.language ?? null;
    this.lineNumbers = options.lineNumbers ?? true;
    this.lineWrapping = options.lineWrapping ?? false;
    this.highlightActiveLine = options.highlightActiveLine ?? true;
    this.onChange = options.onChange ?? null;
    this.onBlur = options.onBlur ?? null;

    // CodeMirror internals (set during init)
    this.view = null;
    this._readOnlyCompartment = null;
    this._themeCompartment = null;
    this._languageCompartment = null;
    this._modules = null;
  }

  /**
   * Initialize the editor with content. Lazy-loads CodeMirror from CDN.
   * @param {string} content - Initial file content
   */
  async init(content = "") {
    this._modules = await loadCore();
    const {
      view: viewMod,
      state: stateMod,
      commands: cmdMod,
      language: langMod,
      search: searchMod,
    } = this._modules;

    // Compartments for dynamic reconfiguration
    this._readOnlyCompartment = new stateMod.Compartment();
    this._themeCompartment = new stateMod.Compartment();
    this._languageCompartment = new stateMod.Compartment();

    // Build extensions list. The line-number, fold, and active-line gutters are
    // grouped behind the `lineNumbers` option so callers can render a clean,
    // gutterless editor (e.g. the beads description field).
    const extensions = [
      ...(this.lineNumbers
        ? [
            viewMod.lineNumbers(),
            viewMod.highlightActiveLineGutter(),
            langMod.foldGutter(),
          ]
        : []),
      ...(this.lineWrapping ? [viewMod.EditorView.lineWrapping] : []),
      ...(this.highlightActiveLine ? [viewMod.highlightActiveLine()] : []),
      viewMod.highlightSpecialChars(),
      viewMod.drawSelection(),
      viewMod.rectangularSelection(),
      viewMod.crosshairCursor(),
      viewMod.dropCursor(),
      stateMod.EditorState.allowMultipleSelections.of(true),
      langMod.indentOnInput(),
      langMod.syntaxHighlighting(langMod.defaultHighlightStyle, {
        fallback: true,
      }),
      langMod.bracketMatching(),
      searchMod.highlightSelectionMatches(),
      viewMod.keymap.of([
        ...cmdMod.defaultKeymap,
        ...cmdMod.historyKeymap,
        ...searchMod.searchKeymap,
        ...langMod.foldKeymap,
        cmdMod.indentWithTab,
      ]),
      cmdMod.history(),

      // Dynamic compartments
      this._readOnlyCompartment.of(
        stateMod.EditorState.readOnly.of(this.readOnly),
      ),
      this._themeCompartment.of(await this._buildThemeExtension()),
      this._languageCompartment.of(await this._buildLanguageExtension()),
    ];

    // Change listener
    if (this.onChange) {
      extensions.push(
        viewMod.EditorView.updateListener.of((update) => {
          if (update.docChanged) {
            this.onChange(update.state.doc.toString());
          }
        }),
      );
    }

    // Blur listener
    if (this.onBlur) {
      extensions.push(
        viewMod.EditorView.domEventHandlers({
          blur: () => {
            this.onBlur(this.getValue());
          },
        }),
      );
    }

    // Font size via CSS custom property on container
    this.container.style.setProperty(
      "--editor-font-size",
      `${this.fontSize}px`,
    );

    // Base theme for font size and scroll
    extensions.push(
      viewMod.EditorView.baseTheme({
        "&": {
          fontSize: "var(--editor-font-size, 13px)",
          height: "100%",
        },
        ".cm-scroller": {
          overflow: "auto",
          fontFamily:
            "ui-monospace, 'SFMono-Regular', 'SF Mono', Menlo, monospace",
        },
        ".cm-gutters": {
          fontSize: "var(--editor-font-size, 13px)",
        },
      }),
    );

    const startState = stateMod.EditorState.create({
      doc: content,
      extensions,
    });
    this.view = new viewMod.EditorView({
      state: startState,
      parent: this.container,
    });
  }

  /** @returns {string} Current document content */
  getValue() {
    return this.view?.state.doc.toString() ?? "";
  }

  /** @param {string} content - Replace entire document */
  setValue(content) {
    if (!this.view) return;
    this.view.dispatch({
      changes: { from: 0, to: this.view.state.doc.length, insert: content },
    });
  }

  /** @param {boolean} readOnly */
  setReadOnly(readOnly) {
    if (!this.view || !this._modules) return;
    this.readOnly = readOnly;
    this.view.dispatch({
      effects: this._readOnlyCompartment.reconfigure(
        this._modules.state.EditorState.readOnly.of(readOnly),
      ),
    });
  }

  /** @param {boolean} dark */
  async setTheme(dark) {
    if (!this.view) return;
    this.darkMode = dark;
    this.view.dispatch({
      effects: this._themeCompartment.reconfigure(
        await this._buildThemeExtension(),
      ),
    });
  }

  /** @param {number} px - Font size in pixels */
  setFontSize(px) {
    this.fontSize = px;
    this.container.style.setProperty("--editor-font-size", `${px}px`);
    if (this.view) this.view.requestMeasure();
  }

  /** @param {string} ext - File extension (e.g., "js", "py") */
  async setLanguage(ext) {
    if (!this.view) return;
    this.language = ext;
    this.view.dispatch({
      effects: this._languageCompartment.reconfigure(
        await this._buildLanguageExtension(),
      ),
    });
  }

  /**
   * Wrap the current selection with `before` and `after` markers.
   * If the selection is empty, inserts `before + placeholder + after` and
   * places the cursor just after `before` (between the markers).
   * @param {string} before       - Text to insert before the selection
   * @param {string} after        - Text to insert after the selection
   * @param {string} [placeholder] - Placeholder text when selection is empty
   */
  wrapSelection(before, after, placeholder = "") {
    if (!this.view) return;
    const { state } = this.view;
    const sel = state.selection.main;
    const isEmpty = sel.from === sel.to;
    if (isEmpty) {
      const insert = before + placeholder + after;
      this.view.dispatch({
        changes: { from: sel.from, to: sel.to, insert },
        selection: {
          anchor: sel.from + before.length,
          head: sel.from + before.length + placeholder.length,
        },
      });
    } else {
      const selectedText = state.doc.sliceString(sel.from, sel.to);
      const insert = before + selectedText + after;
      this.view.dispatch({
        changes: { from: sel.from, to: sel.to, insert },
        selection: {
          anchor: sel.from + before.length,
          head: sel.from + before.length + selectedText.length,
        },
      });
    }
    this.view.focus();
  }

  /**
   * Insert a markdown link, placing the cursor/selection in the URL portion.
   * - Non-empty selection: replaces it with `[<selectedText>](<urlPlaceholder>)`
   *   and selects the `urlPlaceholder` so the user can type the URL immediately.
   * - Empty selection: inserts `[<textPlaceholder>](<urlPlaceholder>)` and
   *   selects `textPlaceholder` so the user types the link text first.
   * @param {string} [textPlaceholder="text"] - Placeholder for the link text
   * @param {string} [urlPlaceholder="url"]   - Placeholder for the URL
   */
  insertLink(textPlaceholder = "text", urlPlaceholder = "url") {
    if (!this.view) return;
    const { state } = this.view;
    const sel = state.selection.main;
    const isEmpty = sel.from === sel.to;
    if (isEmpty) {
      const insert = `[${textPlaceholder}](${urlPlaceholder})`;
      // Select the textPlaceholder so the user types the link text first.
      this.view.dispatch({
        changes: { from: sel.from, to: sel.to, insert },
        selection: {
          anchor: sel.from + 1,
          head: sel.from + 1 + textPlaceholder.length,
        },
      });
    } else {
      const selectedText = state.doc.sliceString(sel.from, sel.to);
      const insert = `[${selectedText}](${urlPlaceholder})`;
      // Cursor lands on the urlPlaceholder (just after the `](`).
      const urlStart = sel.from + 1 + selectedText.length + 2; // `[` + text + `](`
      this.view.dispatch({
        changes: { from: sel.from, to: sel.to, insert },
        selection: { anchor: urlStart, head: urlStart + urlPlaceholder.length },
      });
    }
    this.view.focus();
  }

  /**
   * Prefix each line spanned by the current selection with `marker`.
   * @param {string|Function} marker - String prefix, or `(index: number) => string`
   *   for incrementing prefixes (e.g. numbered lists: `(i) => \`${i + 1}. \``).
   */
  prefixLines(marker) {
    if (!this.view) return;
    const { state } = this.view;
    const sel = state.selection.main;
    const startLine = state.doc.lineAt(sel.from);
    const endLine = state.doc.lineAt(sel.to);
    const changes = [];
    for (
      let lineNum = startLine.number, i = 0;
      lineNum <= endLine.number;
      lineNum++, i++
    ) {
      const line = state.doc.line(lineNum);
      const prefix = typeof marker === "function" ? marker(i) : marker;
      changes.push({ from: line.from, to: line.from, insert: prefix });
    }
    this.view.dispatch({ changes });
    this.view.focus();
  }

  /** Focus the editor. */
  focus() {
    this.view?.focus();
  }

  /**
   * Scroll to and select the start of the given line (1-based). Clamped to
   * document bounds; silently no-ops for invalid input or before init.
   * @param {number} lineNumber - 1-based line number
   */
  scrollToLine(lineNumber) {
    if (!this.view || !this._modules) return;
    const n = Math.floor(Number(lineNumber));
    if (!Number.isFinite(n) || n < 1) return;
    const doc = this.view.state.doc;
    const total = doc.lines;
    const clamped = Math.min(n, total);
    const line = doc.line(clamped);
    const { EditorView } = this._modules.view;
    this.view.dispatch({
      selection: { anchor: line.from, head: line.from },
      effects: EditorView.scrollIntoView(line.from, { y: "center" }),
    });
  }

  /** Destroy the editor and release resources. */
  destroy() {
    if (this.view) {
      this.view.destroy();
      this.view = null;
    }
  }

  /** @returns {boolean} Whether the editor has been initialized */
  isInitialized() {
    return this.view !== null;
  }

  // --- Private helpers ---

  async _buildThemeExtension() {
    if (this.darkMode) {
      const themeMod = await loadDarkTheme();
      return themeMod.oneDark;
    }
    return []; // Default light theme
  }

  async _buildLanguageExtension() {
    if (!this.language) return [];
    const langExt = await loadLanguage(this.language);
    return langExt ?? [];
  }
}
