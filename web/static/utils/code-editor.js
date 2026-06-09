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
   * @param {Function} [options.onChange=null]  - Callback when content changes: (content: string) => void
   */
  constructor(container, options = {}) {
    this.container = container;
    this.readOnly = options.readOnly ?? true;
    this.darkMode = options.darkMode ?? true;
    this.fontSize = options.fontSize ?? 13;
    this.language = options.language ?? null;
    this.onChange = options.onChange ?? null;

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
    const { view: viewMod, state: stateMod, commands: cmdMod, language: langMod, search: searchMod } = this._modules;

    // Compartments for dynamic reconfiguration
    this._readOnlyCompartment = new stateMod.Compartment();
    this._themeCompartment    = new stateMod.Compartment();
    this._languageCompartment = new stateMod.Compartment();

    // Build extensions list
    const extensions = [
      viewMod.lineNumbers(),
      viewMod.highlightActiveLine(),
      viewMod.highlightActiveLineGutter(),
      viewMod.highlightSpecialChars(),
      viewMod.drawSelection(),
      viewMod.rectangularSelection(),
      viewMod.crosshairCursor(),
      viewMod.dropCursor(),
      stateMod.EditorState.allowMultipleSelections.of(true),
      langMod.indentOnInput(),
      langMod.syntaxHighlighting(langMod.defaultHighlightStyle, { fallback: true }),
      langMod.bracketMatching(),
      langMod.foldGutter(),
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
      this._readOnlyCompartment.of(stateMod.EditorState.readOnly.of(this.readOnly)),
      this._themeCompartment.of(await this._buildThemeExtension()),
      this._languageCompartment.of(await this._buildLanguageExtension()),
    ];

    // Change listener
    if (this.onChange) {
      extensions.push(viewMod.EditorView.updateListener.of((update) => {
        if (update.docChanged) {
          this.onChange(update.state.doc.toString());
        }
      }));
    }

    // Font size via CSS custom property on container
    this.container.style.setProperty("--editor-font-size", `${this.fontSize}px`);

    // Base theme for font size and scroll
    extensions.push(viewMod.EditorView.baseTheme({
      "&": {
        fontSize: "var(--editor-font-size, 13px)",
        height: "100%",
      },
      ".cm-scroller": {
        overflow: "auto",
        fontFamily: "ui-monospace, 'SFMono-Regular', 'SF Mono', Menlo, monospace",
      },
      ".cm-gutters": {
        fontSize: "var(--editor-font-size, 13px)",
      },
    }));

    const startState = stateMod.EditorState.create({ doc: content, extensions });
    this.view = new viewMod.EditorView({ state: startState, parent: this.container });
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
        this._modules.state.EditorState.readOnly.of(readOnly)
      ),
    });
  }

  /** @param {boolean} dark */
  async setTheme(dark) {
    if (!this.view) return;
    this.darkMode = dark;
    this.view.dispatch({
      effects: this._themeCompartment.reconfigure(await this._buildThemeExtension()),
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
      effects: this._languageCompartment.reconfigure(await this._buildLanguageExtension()),
    });
  }

  /** Focus the editor. */
  focus() {
    this.view?.focus();
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
