/**
 * Minified by jsDelivr using Terser v5.39.0.
 * Original file: /npm/dompurify@3.0.8/dist/purify.es.mjs
 *
 * Do NOT use SRI with dynamically generated files! More information: https://www.jsdelivr.com/using-sri-with-dynamic-files
 */
/*! @license DOMPurify 3.0.8 | (c) Cure53 and other contributors | Released under the Apache license 2.0 and Mozilla Public License 2.0 | github.com/cure53/DOMPurify/blob/3.0.8/LICENSE */
const {
  entries: entries,
  setPrototypeOf: setPrototypeOf,
  isFrozen: isFrozen,
  getPrototypeOf: getPrototypeOf,
  getOwnPropertyDescriptor: getOwnPropertyDescriptor,
} = Object;
let { freeze: freeze, seal: seal, create: create } = Object,
  { apply: apply, construct: construct } =
    "undefined" != typeof Reflect && Reflect;
(freeze ||
  (freeze = function (e) {
    return e;
  }),
  seal ||
    (seal = function (e) {
      return e;
    }),
  apply ||
    (apply = function (e, t, n) {
      return e.apply(t, n);
    }),
  construct ||
    (construct = function (e, t) {
      return new e(...t);
    }));
const arrayForEach = unapply(Array.prototype.forEach),
  arrayPop = unapply(Array.prototype.pop),
  arrayPush = unapply(Array.prototype.push),
  stringToLowerCase = unapply(String.prototype.toLowerCase),
  stringToString = unapply(String.prototype.toString),
  stringMatch = unapply(String.prototype.match),
  stringReplace = unapply(String.prototype.replace),
  stringIndexOf = unapply(String.prototype.indexOf),
  stringTrim = unapply(String.prototype.trim),
  regExpTest = unapply(RegExp.prototype.test),
  typeErrorCreate = unconstruct(TypeError);
function unapply(e) {
  return function (t) {
    for (
      var n = arguments.length, r = new Array(n > 1 ? n - 1 : 0), o = 1;
      o < n;
      o++
    )
      r[o - 1] = arguments[o];
    return apply(e, t, r);
  };
}
function unconstruct(e) {
  return function () {
    for (var t = arguments.length, n = new Array(t), r = 0; r < t; r++)
      n[r] = arguments[r];
    return construct(e, n);
  };
}
function addToSet(e, t) {
  let n =
    arguments.length > 2 && void 0 !== arguments[2]
      ? arguments[2]
      : stringToLowerCase;
  setPrototypeOf && setPrototypeOf(e, null);
  let r = t.length;
  for (; r--; ) {
    let o = t[r];
    if ("string" == typeof o) {
      const e = n(o);
      e !== o && (isFrozen(t) || (t[r] = e), (o = e));
    }
    e[o] = !0;
  }
  return e;
}
function cleanArray(e) {
  for (let t = 0; t < e.length; t++)
    void 0 === getOwnPropertyDescriptor(e, t) && (e[t] = null);
  return e;
}
function clone(e) {
  const t = create(null);
  for (const [n, r] of entries(e))
    void 0 !== getOwnPropertyDescriptor(e, n) &&
      (Array.isArray(r)
        ? (t[n] = cleanArray(r))
        : r && "object" == typeof r && r.constructor === Object
          ? (t[n] = clone(r))
          : (t[n] = r));
  return t;
}
function lookupGetter(e, t) {
  for (; null !== e; ) {
    const n = getOwnPropertyDescriptor(e, t);
    if (n) {
      if (n.get) return unapply(n.get);
      if ("function" == typeof n.value) return unapply(n.value);
    }
    e = getPrototypeOf(e);
  }
  return function (e) {
    return (console.warn("fallback value for", e), null);
  };
}
const html$1 = freeze([
    "a",
    "abbr",
    "acronym",
    "address",
    "area",
    "article",
    "aside",
    "audio",
    "b",
    "bdi",
    "bdo",
    "big",
    "blink",
    "blockquote",
    "body",
    "br",
    "button",
    "canvas",
    "caption",
    "center",
    "cite",
    "code",
    "col",
    "colgroup",
    "content",
    "data",
    "datalist",
    "dd",
    "decorator",
    "del",
    "details",
    "dfn",
    "dialog",
    "dir",
    "div",
    "dl",
    "dt",
    "element",
    "em",
    "fieldset",
    "figcaption",
    "figure",
    "font",
    "footer",
    "form",
    "h1",
    "h2",
    "h3",
    "h4",
    "h5",
    "h6",
    "head",
    "header",
    "hgroup",
    "hr",
    "html",
    "i",
    "img",
    "input",
    "ins",
    "kbd",
    "label",
    "legend",
    "li",
    "main",
    "map",
    "mark",
    "marquee",
    "menu",
    "menuitem",
    "meter",
    "nav",
    "nobr",
    "ol",
    "optgroup",
    "option",
    "output",
    "p",
    "picture",
    "pre",
    "progress",
    "q",
    "rp",
    "rt",
    "ruby",
    "s",
    "samp",
    "section",
    "select",
    "shadow",
    "small",
    "source",
    "spacer",
    "span",
    "strike",
    "strong",
    "style",
    "sub",
    "summary",
    "sup",
    "table",
    "tbody",
    "td",
    "template",
    "textarea",
    "tfoot",
    "th",
    "thead",
    "time",
    "tr",
    "track",
    "tt",
    "u",
    "ul",
    "var",
    "video",
    "wbr",
  ]),
  svg$1 = freeze([
    "svg",
    "a",
    "altglyph",
    "altglyphdef",
    "altglyphitem",
    "animatecolor",
    "animatemotion",
    "animatetransform",
    "circle",
    "clippath",
    "defs",
    "desc",
    "ellipse",
    "filter",
    "font",
    "g",
    "glyph",
    "glyphref",
    "hkern",
    "image",
    "line",
    "lineargradient",
    "marker",
    "mask",
    "metadata",
    "mpath",
    "path",
    "pattern",
    "polygon",
    "polyline",
    "radialgradient",
    "rect",
    "stop",
    "style",
    "switch",
    "symbol",
    "text",
    "textpath",
    "title",
    "tref",
    "tspan",
    "view",
    "vkern",
  ]),
  svgFilters = freeze([
    "feBlend",
    "feColorMatrix",
    "feComponentTransfer",
    "feComposite",
    "feConvolveMatrix",
    "feDiffuseLighting",
    "feDisplacementMap",
    "feDistantLight",
    "feDropShadow",
    "feFlood",
    "feFuncA",
    "feFuncB",
    "feFuncG",
    "feFuncR",
    "feGaussianBlur",
    "feImage",
    "feMerge",
    "feMergeNode",
    "feMorphology",
    "feOffset",
    "fePointLight",
    "feSpecularLighting",
    "feSpotLight",
    "feTile",
    "feTurbulence",
  ]),
  svgDisallowed = freeze([
    "animate",
    "color-profile",
    "cursor",
    "discard",
    "font-face",
    "font-face-format",
    "font-face-name",
    "font-face-src",
    "font-face-uri",
    "foreignobject",
    "hatch",
    "hatchpath",
    "mesh",
    "meshgradient",
    "meshpatch",
    "meshrow",
    "missing-glyph",
    "script",
    "set",
    "solidcolor",
    "unknown",
    "use",
  ]),
  mathMl$1 = freeze([
    "math",
    "menclose",
    "merror",
    "mfenced",
    "mfrac",
    "mglyph",
    "mi",
    "mlabeledtr",
    "mmultiscripts",
    "mn",
    "mo",
    "mover",
    "mpadded",
    "mphantom",
    "mroot",
    "mrow",
    "ms",
    "mspace",
    "msqrt",
    "mstyle",
    "msub",
    "msup",
    "msubsup",
    "mtable",
    "mtd",
    "mtext",
    "mtr",
    "munder",
    "munderover",
    "mprescripts",
  ]),
  mathMlDisallowed = freeze([
    "maction",
    "maligngroup",
    "malignmark",
    "mlongdiv",
    "mscarries",
    "mscarry",
    "msgroup",
    "mstack",
    "msline",
    "msrow",
    "semantics",
    "annotation",
    "annotation-xml",
    "mprescripts",
    "none",
  ]),
  text = freeze(["#text"]),
  html = freeze([
    "accept",
    "action",
    "align",
    "alt",
    "autocapitalize",
    "autocomplete",
    "autopictureinpicture",
    "autoplay",
    "background",
    "bgcolor",
    "border",
    "capture",
    "cellpadding",
    "cellspacing",
    "checked",
    "cite",
    "class",
    "clear",
    "color",
    "cols",
    "colspan",
    "controls",
    "controlslist",
    "coords",
    "crossorigin",
    "datetime",
    "decoding",
    "default",
    "dir",
    "disabled",
    "disablepictureinpicture",
    "disableremoteplayback",
    "download",
    "draggable",
    "enctype",
    "enterkeyhint",
    "face",
    "for",
    "headers",
    "height",
    "hidden",
    "high",
    "href",
    "hreflang",
    "id",
    "inputmode",
    "integrity",
    "ismap",
    "kind",
    "label",
    "lang",
    "list",
    "loading",
    "loop",
    "low",
    "max",
    "maxlength",
    "media",
    "method",
    "min",
    "minlength",
    "multiple",
    "muted",
    "name",
    "nonce",
    "noshade",
    "novalidate",
    "nowrap",
    "open",
    "optimum",
    "pattern",
    "placeholder",
    "playsinline",
    "poster",
    "preload",
    "pubdate",
    "radiogroup",
    "readonly",
    "rel",
    "required",
    "rev",
    "reversed",
    "role",
    "rows",
    "rowspan",
    "spellcheck",
    "scope",
    "selected",
    "shape",
    "size",
    "sizes",
    "span",
    "srclang",
    "start",
    "src",
    "srcset",
    "step",
    "style",
    "summary",
    "tabindex",
    "title",
    "translate",
    "type",
    "usemap",
    "valign",
    "value",
    "width",
    "xmlns",
    "slot",
  ]),
  svg = freeze([
    "accent-height",
    "accumulate",
    "additive",
    "alignment-baseline",
    "ascent",
    "attributename",
    "attributetype",
    "azimuth",
    "basefrequency",
    "baseline-shift",
    "begin",
    "bias",
    "by",
    "class",
    "clip",
    "clippathunits",
    "clip-path",
    "clip-rule",
    "color",
    "color-interpolation",
    "color-interpolation-filters",
    "color-profile",
    "color-rendering",
    "cx",
    "cy",
    "d",
    "dx",
    "dy",
    "diffuseconstant",
    "direction",
    "display",
    "divisor",
    "dur",
    "edgemode",
    "elevation",
    "end",
    "fill",
    "fill-opacity",
    "fill-rule",
    "filter",
    "filterunits",
    "flood-color",
    "flood-opacity",
    "font-family",
    "font-size",
    "font-size-adjust",
    "font-stretch",
    "font-style",
    "font-variant",
    "font-weight",
    "fx",
    "fy",
    "g1",
    "g2",
    "glyph-name",
    "glyphref",
    "gradientunits",
    "gradienttransform",
    "height",
    "href",
    "id",
    "image-rendering",
    "in",
    "in2",
    "k",
    "k1",
    "k2",
    "k3",
    "k4",
    "kerning",
    "keypoints",
    "keysplines",
    "keytimes",
    "lang",
    "lengthadjust",
    "letter-spacing",
    "kernelmatrix",
    "kernelunitlength",
    "lighting-color",
    "local",
    "marker-end",
    "marker-mid",
    "marker-start",
    "markerheight",
    "markerunits",
    "markerwidth",
    "maskcontentunits",
    "maskunits",
    "max",
    "mask",
    "media",
    "method",
    "mode",
    "min",
    "name",
    "numoctaves",
    "offset",
    "operator",
    "opacity",
    "order",
    "orient",
    "orientation",
    "origin",
    "overflow",
    "paint-order",
    "path",
    "pathlength",
    "patterncontentunits",
    "patterntransform",
    "patternunits",
    "points",
    "preservealpha",
    "preserveaspectratio",
    "primitiveunits",
    "r",
    "rx",
    "ry",
    "radius",
    "refx",
    "refy",
    "repeatcount",
    "repeatdur",
    "restart",
    "result",
    "rotate",
    "scale",
    "seed",
    "shape-rendering",
    "specularconstant",
    "specularexponent",
    "spreadmethod",
    "startoffset",
    "stddeviation",
    "stitchtiles",
    "stop-color",
    "stop-opacity",
    "stroke-dasharray",
    "stroke-dashoffset",
    "stroke-linecap",
    "stroke-linejoin",
    "stroke-miterlimit",
    "stroke-opacity",
    "stroke",
    "stroke-width",
    "style",
    "surfacescale",
    "systemlanguage",
    "tabindex",
    "targetx",
    "targety",
    "transform",
    "transform-origin",
    "text-anchor",
    "text-decoration",
    "text-rendering",
    "textlength",
    "type",
    "u1",
    "u2",
    "unicode",
    "values",
    "viewbox",
    "visibility",
    "version",
    "vert-adv-y",
    "vert-origin-x",
    "vert-origin-y",
    "width",
    "word-spacing",
    "wrap",
    "writing-mode",
    "xchannelselector",
    "ychannelselector",
    "x",
    "x1",
    "x2",
    "xmlns",
    "y",
    "y1",
    "y2",
    "z",
    "zoomandpan",
  ]),
  mathMl = freeze([
    "accent",
    "accentunder",
    "align",
    "bevelled",
    "close",
    "columnsalign",
    "columnlines",
    "columnspan",
    "denomalign",
    "depth",
    "dir",
    "display",
    "displaystyle",
    "encoding",
    "fence",
    "frame",
    "height",
    "href",
    "id",
    "largeop",
    "length",
    "linethickness",
    "lspace",
    "lquote",
    "mathbackground",
    "mathcolor",
    "mathsize",
    "mathvariant",
    "maxsize",
    "minsize",
    "movablelimits",
    "notation",
    "numalign",
    "open",
    "rowalign",
    "rowlines",
    "rowspacing",
    "rowspan",
    "rspace",
    "rquote",
    "scriptlevel",
    "scriptminsize",
    "scriptsizemultiplier",
    "selection",
    "separator",
    "separators",
    "stretchy",
    "subscriptshift",
    "supscriptshift",
    "symmetric",
    "voffset",
    "width",
    "xmlns",
  ]),
  xml = freeze([
    "xlink:href",
    "xml:id",
    "xlink:title",
    "xml:space",
    "xmlns:xlink",
  ]),
  MUSTACHE_EXPR = seal(/\{\{[\w\W]*|[\w\W]*\}\}/gm),
  ERB_EXPR = seal(/<%[\w\W]*|[\w\W]*%>/gm),
  TMPLIT_EXPR = seal(/\${[\w\W]*}/gm),
  DATA_ATTR = seal(/^data-[\-\w.\u00B7-\uFFFF]/),
  ARIA_ATTR = seal(/^aria-[\-\w]+$/),
  IS_ALLOWED_URI = seal(
    /^(?:(?:(?:f|ht)tps?|mailto|tel|callto|sms|cid|xmpp):|[^a-z]|[a-z+.\-]+(?:[^a-z+.\-:]|$))/i,
  ),
  IS_SCRIPT_OR_DATA = seal(/^(?:\w+script|data):/i),
  ATTR_WHITESPACE = seal(
    /[\u0000-\u0020\u00A0\u1680\u180E\u2000-\u2029\u205F\u3000]/g,
  ),
  DOCTYPE_NAME = seal(/^html$/i);
var EXPRESSIONS = Object.freeze({
  __proto__: null,
  MUSTACHE_EXPR: MUSTACHE_EXPR,
  ERB_EXPR: ERB_EXPR,
  TMPLIT_EXPR: TMPLIT_EXPR,
  DATA_ATTR: DATA_ATTR,
  ARIA_ATTR: ARIA_ATTR,
  IS_ALLOWED_URI: IS_ALLOWED_URI,
  IS_SCRIPT_OR_DATA: IS_SCRIPT_OR_DATA,
  ATTR_WHITESPACE: ATTR_WHITESPACE,
  DOCTYPE_NAME: DOCTYPE_NAME,
});
const getGlobal = function () {
    return "undefined" == typeof window ? null : window;
  },
  _createTrustedTypesPolicy = function (e, t) {
    if ("object" != typeof e || "function" != typeof e.createPolicy)
      return null;
    let n = null;
    const r = "data-tt-policy-suffix";
    t && t.hasAttribute(r) && (n = t.getAttribute(r));
    const o = "dompurify" + (n ? "#" + n : "");
    try {
      return e.createPolicy(o, {
        createHTML: (e) => e,
        createScriptURL: (e) => e,
      });
    } catch (e) {
      return (
        console.warn("TrustedTypes policy " + o + " could not be created."),
        null
      );
    }
  };
function createDOMPurify() {
  let e =
    arguments.length > 0 && void 0 !== arguments[0]
      ? arguments[0]
      : getGlobal();
  const t = (e) => createDOMPurify(e);
  if (
    ((t.version = "3.0.8"),
    (t.removed = []),
    !e || !e.document || 9 !== e.document.nodeType)
  )
    return ((t.isSupported = !1), t);
  let { document: n } = e;
  const r = n,
    o = r.currentScript,
    {
      DocumentFragment: a,
      HTMLTemplateElement: i,
      Node: l,
      Element: s,
      NodeFilter: c,
      NamedNodeMap: p = e.NamedNodeMap || e.MozNamedAttrMap,
      HTMLFormElement: u,
      DOMParser: m,
      trustedTypes: d,
    } = e,
    f = s.prototype,
    g = lookupGetter(f, "cloneNode"),
    T = lookupGetter(f, "nextSibling"),
    h = lookupGetter(f, "childNodes"),
    y = lookupGetter(f, "parentNode");
  if ("function" == typeof i) {
    const e = n.createElement("template");
    e.content && e.content.ownerDocument && (n = e.content.ownerDocument);
  }
  let E,
    S = "";
  const {
      implementation: A,
      createNodeIterator: _,
      createDocumentFragment: R,
      getElementsByTagName: N,
    } = n,
    { importNode: b } = r;
  let D = {};
  t.isSupported =
    "function" == typeof entries &&
    "function" == typeof y &&
    A &&
    void 0 !== A.createHTMLDocument;
  const {
    MUSTACHE_EXPR: x,
    ERB_EXPR: w,
    TMPLIT_EXPR: C,
    DATA_ATTR: O,
    ARIA_ATTR: v,
    IS_SCRIPT_OR_DATA: L,
    ATTR_WHITESPACE: I,
  } = EXPRESSIONS;
  let { IS_ALLOWED_URI: M } = EXPRESSIONS,
    k = null;
  const P = addToSet({}, [
    ...html$1,
    ...svg$1,
    ...svgFilters,
    ...mathMl$1,
    ...text,
  ]);
  let U = null;
  const z = addToSet({}, [...html, ...svg, ...mathMl, ...xml]);
  let F = Object.seal(
      create(null, {
        tagNameCheck: {
          writable: !0,
          configurable: !1,
          enumerable: !0,
          value: null,
        },
        attributeNameCheck: {
          writable: !0,
          configurable: !1,
          enumerable: !0,
          value: null,
        },
        allowCustomizedBuiltInElements: {
          writable: !0,
          configurable: !1,
          enumerable: !0,
          value: !1,
        },
      }),
    ),
    H = null,
    W = null,
    B = !0,
    G = !0,
    Y = !1,
    X = !0,
    j = !1,
    $ = !1,
    q = !1,
    K = !1,
    V = !1,
    Z = !1,
    J = !1,
    Q = !0,
    ee = !1;
  let te = !0,
    ne = !1,
    re = {},
    oe = null;
  const ae = addToSet({}, [
    "annotation-xml",
    "audio",
    "colgroup",
    "desc",
    "foreignobject",
    "head",
    "iframe",
    "math",
    "mi",
    "mn",
    "mo",
    "ms",
    "mtext",
    "noembed",
    "noframes",
    "noscript",
    "plaintext",
    "script",
    "style",
    "svg",
    "template",
    "thead",
    "title",
    "video",
    "xmp",
  ]);
  let ie = null;
  const le = addToSet({}, [
    "audio",
    "video",
    "img",
    "source",
    "image",
    "track",
  ]);
  let se = null;
  const ce = addToSet({}, [
      "alt",
      "class",
      "for",
      "id",
      "label",
      "name",
      "pattern",
      "placeholder",
      "role",
      "summary",
      "title",
      "value",
      "style",
      "xmlns",
    ]),
    pe = "http://www.w3.org/1998/Math/MathML",
    ue = "http://www.w3.org/2000/svg",
    me = "http://www.w3.org/1999/xhtml";
  let de = me,
    fe = !1,
    ge = null;
  const Te = addToSet({}, [pe, ue, me], stringToString);
  let he = null;
  const ye = ["application/xhtml+xml", "text/html"];
  let Ee = null,
    Se = null;
  const Ae = n.createElement("form"),
    _e = function (e) {
      return e instanceof RegExp || e instanceof Function;
    },
    Re = function () {
      let e =
        arguments.length > 0 && void 0 !== arguments[0] ? arguments[0] : {};
      if (!Se || Se !== e) {
        if (
          ((e && "object" == typeof e) || (e = {}),
          (e = clone(e)),
          (he =
            -1 === ye.indexOf(e.PARSER_MEDIA_TYPE)
              ? "text/html"
              : e.PARSER_MEDIA_TYPE),
          (Ee =
            "application/xhtml+xml" === he
              ? stringToString
              : stringToLowerCase),
          (k = "ALLOWED_TAGS" in e ? addToSet({}, e.ALLOWED_TAGS, Ee) : P),
          (U = "ALLOWED_ATTR" in e ? addToSet({}, e.ALLOWED_ATTR, Ee) : z),
          (ge =
            "ALLOWED_NAMESPACES" in e
              ? addToSet({}, e.ALLOWED_NAMESPACES, stringToString)
              : Te),
          (se =
            "ADD_URI_SAFE_ATTR" in e
              ? addToSet(clone(ce), e.ADD_URI_SAFE_ATTR, Ee)
              : ce),
          (ie =
            "ADD_DATA_URI_TAGS" in e
              ? addToSet(clone(le), e.ADD_DATA_URI_TAGS, Ee)
              : le),
          (oe =
            "FORBID_CONTENTS" in e ? addToSet({}, e.FORBID_CONTENTS, Ee) : ae),
          (H = "FORBID_TAGS" in e ? addToSet({}, e.FORBID_TAGS, Ee) : {}),
          (W = "FORBID_ATTR" in e ? addToSet({}, e.FORBID_ATTR, Ee) : {}),
          (re = "USE_PROFILES" in e && e.USE_PROFILES),
          (B = !1 !== e.ALLOW_ARIA_ATTR),
          (G = !1 !== e.ALLOW_DATA_ATTR),
          (Y = e.ALLOW_UNKNOWN_PROTOCOLS || !1),
          (X = !1 !== e.ALLOW_SELF_CLOSE_IN_ATTR),
          (j = e.SAFE_FOR_TEMPLATES || !1),
          ($ = e.WHOLE_DOCUMENT || !1),
          (V = e.RETURN_DOM || !1),
          (Z = e.RETURN_DOM_FRAGMENT || !1),
          (J = e.RETURN_TRUSTED_TYPE || !1),
          (K = e.FORCE_BODY || !1),
          (Q = !1 !== e.SANITIZE_DOM),
          (ee = e.SANITIZE_NAMED_PROPS || !1),
          (te = !1 !== e.KEEP_CONTENT),
          (ne = e.IN_PLACE || !1),
          (M = e.ALLOWED_URI_REGEXP || IS_ALLOWED_URI),
          (de = e.NAMESPACE || me),
          (F = e.CUSTOM_ELEMENT_HANDLING || {}),
          e.CUSTOM_ELEMENT_HANDLING &&
            _e(e.CUSTOM_ELEMENT_HANDLING.tagNameCheck) &&
            (F.tagNameCheck = e.CUSTOM_ELEMENT_HANDLING.tagNameCheck),
          e.CUSTOM_ELEMENT_HANDLING &&
            _e(e.CUSTOM_ELEMENT_HANDLING.attributeNameCheck) &&
            (F.attributeNameCheck =
              e.CUSTOM_ELEMENT_HANDLING.attributeNameCheck),
          e.CUSTOM_ELEMENT_HANDLING &&
            "boolean" ==
              typeof e.CUSTOM_ELEMENT_HANDLING.allowCustomizedBuiltInElements &&
            (F.allowCustomizedBuiltInElements =
              e.CUSTOM_ELEMENT_HANDLING.allowCustomizedBuiltInElements),
          j && (G = !1),
          Z && (V = !0),
          re &&
            ((k = addToSet({}, text)),
            (U = []),
            !0 === re.html && (addToSet(k, html$1), addToSet(U, html)),
            !0 === re.svg &&
              (addToSet(k, svg$1), addToSet(U, svg), addToSet(U, xml)),
            !0 === re.svgFilters &&
              (addToSet(k, svgFilters), addToSet(U, svg), addToSet(U, xml)),
            !0 === re.mathMl &&
              (addToSet(k, mathMl$1), addToSet(U, mathMl), addToSet(U, xml))),
          e.ADD_TAGS &&
            (k === P && (k = clone(k)), addToSet(k, e.ADD_TAGS, Ee)),
          e.ADD_ATTR &&
            (U === z && (U = clone(U)), addToSet(U, e.ADD_ATTR, Ee)),
          e.ADD_URI_SAFE_ATTR && addToSet(se, e.ADD_URI_SAFE_ATTR, Ee),
          e.FORBID_CONTENTS &&
            (oe === ae && (oe = clone(oe)),
            addToSet(oe, e.FORBID_CONTENTS, Ee)),
          te && (k["#text"] = !0),
          $ && addToSet(k, ["html", "head", "body"]),
          k.table && (addToSet(k, ["tbody"]), delete H.tbody),
          e.TRUSTED_TYPES_POLICY)
        ) {
          if ("function" != typeof e.TRUSTED_TYPES_POLICY.createHTML)
            throw typeErrorCreate(
              'TRUSTED_TYPES_POLICY configuration option must provide a "createHTML" hook.',
            );
          if ("function" != typeof e.TRUSTED_TYPES_POLICY.createScriptURL)
            throw typeErrorCreate(
              'TRUSTED_TYPES_POLICY configuration option must provide a "createScriptURL" hook.',
            );
          ((E = e.TRUSTED_TYPES_POLICY), (S = E.createHTML("")));
        } else
          (void 0 === E && (E = _createTrustedTypesPolicy(d, o)),
            null !== E && "string" == typeof S && (S = E.createHTML("")));
        (freeze && freeze(e), (Se = e));
      }
    },
    Ne = addToSet({}, ["mi", "mo", "mn", "ms", "mtext"]),
    be = addToSet({}, ["foreignobject", "desc", "title", "annotation-xml"]),
    De = addToSet({}, ["title", "style", "font", "a", "script"]),
    xe = addToSet({}, [...svg$1, ...svgFilters, ...svgDisallowed]),
    we = addToSet({}, [...mathMl$1, ...mathMlDisallowed]),
    Ce = function (e) {
      arrayPush(t.removed, { element: e });
      try {
        e.parentNode.removeChild(e);
      } catch (t) {
        e.remove();
      }
    },
    Oe = function (e, n) {
      try {
        arrayPush(t.removed, { attribute: n.getAttributeNode(e), from: n });
      } catch (e) {
        arrayPush(t.removed, { attribute: null, from: n });
      }
      if ((n.removeAttribute(e), "is" === e && !U[e]))
        if (V || Z)
          try {
            Ce(n);
          } catch (e) {}
        else
          try {
            n.setAttribute(e, "");
          } catch (e) {}
    },
    ve = function (e) {
      let t = null,
        r = null;
      if (K) e = "<remove></remove>" + e;
      else {
        const t = stringMatch(e, /^[\r\n\t ]+/);
        r = t && t[0];
      }
      "application/xhtml+xml" === he &&
        de === me &&
        (e =
          '<html xmlns="http://www.w3.org/1999/xhtml"><head></head><body>' +
          e +
          "</body></html>");
      const o = E ? E.createHTML(e) : e;
      if (de === me)
        try {
          t = new m().parseFromString(o, he);
        } catch (e) {}
      if (!t || !t.documentElement) {
        t = A.createDocument(de, "template", null);
        try {
          t.documentElement.innerHTML = fe ? S : o;
        } catch (e) {}
      }
      const a = t.body || t.documentElement;
      return (
        e && r && a.insertBefore(n.createTextNode(r), a.childNodes[0] || null),
        de === me
          ? N.call(t, $ ? "html" : "body")[0]
          : $
            ? t.documentElement
            : a
      );
    },
    Le = function (e) {
      return _.call(
        e.ownerDocument || e,
        e,
        c.SHOW_ELEMENT | c.SHOW_COMMENT | c.SHOW_TEXT,
        null,
      );
    },
    Ie = function (e) {
      return "function" == typeof l && e instanceof l;
    },
    Me = function (e, n, r) {
      D[e] &&
        arrayForEach(D[e], (e) => {
          e.call(t, n, r, Se);
        });
    },
    ke = function (e) {
      let n = null;
      if (
        (Me("beforeSanitizeElements", e, null),
        (r = e) instanceof u &&
          ("string" != typeof r.nodeName ||
            "string" != typeof r.textContent ||
            "function" != typeof r.removeChild ||
            !(r.attributes instanceof p) ||
            "function" != typeof r.removeAttribute ||
            "function" != typeof r.setAttribute ||
            "string" != typeof r.namespaceURI ||
            "function" != typeof r.insertBefore ||
            "function" != typeof r.hasChildNodes))
      )
        return (Ce(e), !0);
      var r;
      const o = Ee(e.nodeName);
      if (
        (Me("uponSanitizeElement", e, { tagName: o, allowedTags: k }),
        e.hasChildNodes() &&
          !Ie(e.firstElementChild) &&
          regExpTest(/<[/\w]/g, e.innerHTML) &&
          regExpTest(/<[/\w]/g, e.textContent))
      )
        return (Ce(e), !0);
      if (!k[o] || H[o]) {
        if (!H[o] && Ue(o)) {
          if (F.tagNameCheck instanceof RegExp && regExpTest(F.tagNameCheck, o))
            return !1;
          if (F.tagNameCheck instanceof Function && F.tagNameCheck(o))
            return !1;
        }
        if (te && !oe[o]) {
          const t = y(e) || e.parentNode,
            n = h(e) || e.childNodes;
          if (n && t) {
            for (let r = n.length - 1; r >= 0; --r)
              t.insertBefore(g(n[r], !0), T(e));
          }
        }
        return (Ce(e), !0);
      }
      return e instanceof s &&
        !(function (e) {
          let t = y(e);
          (t && t.tagName) || (t = { namespaceURI: de, tagName: "template" });
          const n = stringToLowerCase(e.tagName),
            r = stringToLowerCase(t.tagName);
          return (
            !!ge[e.namespaceURI] &&
            (e.namespaceURI === ue
              ? t.namespaceURI === me
                ? "svg" === n
                : t.namespaceURI === pe
                  ? "svg" === n && ("annotation-xml" === r || Ne[r])
                  : Boolean(xe[n])
              : e.namespaceURI === pe
                ? t.namespaceURI === me
                  ? "math" === n
                  : t.namespaceURI === ue
                    ? "math" === n && be[r]
                    : Boolean(we[n])
                : e.namespaceURI === me
                  ? !(t.namespaceURI === ue && !be[r]) &&
                    !(t.namespaceURI === pe && !Ne[r]) &&
                    !we[n] &&
                    (De[n] || !xe[n])
                  : !("application/xhtml+xml" !== he || !ge[e.namespaceURI]))
          );
        })(e)
        ? (Ce(e), !0)
        : ("noscript" !== o && "noembed" !== o && "noframes" !== o) ||
            !regExpTest(/<\/no(script|embed|frames)/i, e.innerHTML)
          ? (j &&
              3 === e.nodeType &&
              ((n = e.textContent),
              arrayForEach([x, w, C], (e) => {
                n = stringReplace(n, e, " ");
              }),
              e.textContent !== n &&
                (arrayPush(t.removed, { element: e.cloneNode() }),
                (e.textContent = n))),
            Me("afterSanitizeElements", e, null),
            !1)
          : (Ce(e), !0);
    },
    Pe = function (e, t, r) {
      if (Q && ("id" === t || "name" === t) && (r in n || r in Ae)) return !1;
      if (G && !W[t] && regExpTest(O, t));
      else if (B && regExpTest(v, t));
      else if (!U[t] || W[t]) {
        if (
          !(
            (Ue(e) &&
              ((F.tagNameCheck instanceof RegExp &&
                regExpTest(F.tagNameCheck, e)) ||
                (F.tagNameCheck instanceof Function && F.tagNameCheck(e))) &&
              ((F.attributeNameCheck instanceof RegExp &&
                regExpTest(F.attributeNameCheck, t)) ||
                (F.attributeNameCheck instanceof Function &&
                  F.attributeNameCheck(t)))) ||
            ("is" === t &&
              F.allowCustomizedBuiltInElements &&
              ((F.tagNameCheck instanceof RegExp &&
                regExpTest(F.tagNameCheck, r)) ||
                (F.tagNameCheck instanceof Function && F.tagNameCheck(r))))
          )
        )
          return !1;
      } else if (se[t]);
      else if (regExpTest(M, stringReplace(r, I, "")));
      else if (
        ("src" !== t && "xlink:href" !== t && "href" !== t) ||
        "script" === e ||
        0 !== stringIndexOf(r, "data:") ||
        !ie[e]
      ) {
        if (Y && !regExpTest(L, stringReplace(r, I, "")));
        else if (r) return !1;
      } else;
      return !0;
    },
    Ue = function (e) {
      return e.indexOf("-") > 0;
    },
    ze = function (e) {
      Me("beforeSanitizeAttributes", e, null);
      const { attributes: n } = e;
      if (!n) return;
      const r = {
        attrName: "",
        attrValue: "",
        keepAttr: !0,
        allowedAttributes: U,
      };
      let o = n.length;
      for (; o--; ) {
        const a = n[o],
          { name: i, namespaceURI: l, value: s } = a,
          c = Ee(i);
        let p = "value" === i ? s : stringTrim(s);
        if (
          ((r.attrName = c),
          (r.attrValue = p),
          (r.keepAttr = !0),
          (r.forceKeepAttr = void 0),
          Me("uponSanitizeAttribute", e, r),
          (p = r.attrValue),
          r.forceKeepAttr)
        )
          continue;
        if ((Oe(i, e), !r.keepAttr)) continue;
        if (!X && regExpTest(/\/>/i, p)) {
          Oe(i, e);
          continue;
        }
        j &&
          arrayForEach([x, w, C], (e) => {
            p = stringReplace(p, e, " ");
          });
        const u = Ee(e.nodeName);
        if (Pe(u, c, p)) {
          if (
            (!ee ||
              ("id" !== c && "name" !== c) ||
              (Oe(i, e), (p = "user-content-" + p)),
            E &&
              "object" == typeof d &&
              "function" == typeof d.getAttributeType)
          )
            if (l);
            else
              switch (d.getAttributeType(u, c)) {
                case "TrustedHTML":
                  p = E.createHTML(p);
                  break;
                case "TrustedScriptURL":
                  p = E.createScriptURL(p);
              }
          try {
            (l ? e.setAttributeNS(l, i, p) : e.setAttribute(i, p),
              arrayPop(t.removed));
          } catch (e) {}
        }
      }
      Me("afterSanitizeAttributes", e, null);
    },
    Fe = function e(t) {
      let n = null;
      const r = Le(t);
      for (Me("beforeSanitizeShadowDOM", t, null); (n = r.nextNode()); )
        (Me("uponSanitizeShadowNode", n, null),
          ke(n) || (n.content instanceof a && e(n.content), ze(n)));
      Me("afterSanitizeShadowDOM", t, null);
    };
  return (
    (t.sanitize = function (e) {
      let n =
          arguments.length > 1 && void 0 !== arguments[1] ? arguments[1] : {},
        o = null,
        i = null,
        s = null,
        c = null;
      if (
        ((fe = !e), fe && (e = "\x3c!--\x3e"), "string" != typeof e && !Ie(e))
      ) {
        if ("function" != typeof e.toString)
          throw typeErrorCreate("toString is not a function");
        if ("string" != typeof (e = e.toString()))
          throw typeErrorCreate("dirty is not a string, aborting");
      }
      if (!t.isSupported) return e;
      if (
        (q || Re(n), (t.removed = []), "string" == typeof e && (ne = !1), ne)
      ) {
        if (e.nodeName) {
          const t = Ee(e.nodeName);
          if (!k[t] || H[t])
            throw typeErrorCreate(
              "root node is forbidden and cannot be sanitized in-place",
            );
        }
      } else if (e instanceof l)
        ((o = ve("\x3c!----\x3e")),
          (i = o.ownerDocument.importNode(e, !0)),
          (1 === i.nodeType && "BODY" === i.nodeName) || "HTML" === i.nodeName
            ? (o = i)
            : o.appendChild(i));
      else {
        if (!V && !j && !$ && -1 === e.indexOf("<"))
          return E && J ? E.createHTML(e) : e;
        if (((o = ve(e)), !o)) return V ? null : J ? S : "";
      }
      o && K && Ce(o.firstChild);
      const p = Le(ne ? e : o);
      for (; (s = p.nextNode()); )
        ke(s) || (s.content instanceof a && Fe(s.content), ze(s));
      if (ne) return e;
      if (V) {
        if (Z)
          for (c = R.call(o.ownerDocument); o.firstChild; )
            c.appendChild(o.firstChild);
        else c = o;
        return (
          (U.shadowroot || U.shadowrootmode) && (c = b.call(r, c, !0)),
          c
        );
      }
      let u = $ ? o.outerHTML : o.innerHTML;
      return (
        $ &&
          k["!doctype"] &&
          o.ownerDocument &&
          o.ownerDocument.doctype &&
          o.ownerDocument.doctype.name &&
          regExpTest(DOCTYPE_NAME, o.ownerDocument.doctype.name) &&
          (u = "<!DOCTYPE " + o.ownerDocument.doctype.name + ">\n" + u),
        j &&
          arrayForEach([x, w, C], (e) => {
            u = stringReplace(u, e, " ");
          }),
        E && J ? E.createHTML(u) : u
      );
    }),
    (t.setConfig = function () {
      (Re(arguments.length > 0 && void 0 !== arguments[0] ? arguments[0] : {}),
        (q = !0));
    }),
    (t.clearConfig = function () {
      ((Se = null), (q = !1));
    }),
    (t.isValidAttribute = function (e, t, n) {
      Se || Re({});
      const r = Ee(e),
        o = Ee(t);
      return Pe(r, o, n);
    }),
    (t.addHook = function (e, t) {
      "function" == typeof t && ((D[e] = D[e] || []), arrayPush(D[e], t));
    }),
    (t.removeHook = function (e) {
      if (D[e]) return arrayPop(D[e]);
    }),
    (t.removeHooks = function (e) {
      D[e] && (D[e] = []);
    }),
    (t.removeAllHooks = function () {
      D = {};
    }),
    t
  );
}
var purify = createDOMPurify();
export { purify as default };
//# sourceMappingURL=/sm/306468185600b1329e1f357ed594d68734f7b263a7acadfb04556a9cd966f41f.map
