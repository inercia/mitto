#ifndef TEXTSUBSTITUTION_DARWIN_H
#define TEXTSUBSTITUTION_DARWIN_H

// disableTextSubstitutions disables macOS automatic text substitutions
// (smart dashes, smart quotes, text replacement) for this application.
// Must be called before creating the WKWebView.
void disableTextSubstitutions(void);

#endif
