// textsubstitution_darwin.m - Disable macOS automatic text substitutions
//
// macOS automatically converts "--" to "—" (em dash) and similar text
// substitutions in WKWebView text inputs. This is controlled by per-app
// NSUserDefaults keys. Setting them to NO disables these substitutions
// for the Mitto app only (does not affect system-wide settings).

#import <Cocoa/Cocoa.h>
#include "textsubstitution_darwin.h"

void disableTextSubstitutions(void) {
    @autoreleasepool {
        NSUserDefaults *defaults = [NSUserDefaults standardUserDefaults];
        
        // Disable smart dashes ("--" -> "—")
        [defaults setBool:NO forKey:@"NSAutomaticDashSubstitutionEnabled"];
        [defaults setBool:NO forKey:@"WebAutomaticDashSubstitutionEnabled"];
        
        // Disable smart quotes ("..." -> "...")
        [defaults setBool:NO forKey:@"NSAutomaticQuoteSubstitutionEnabled"];
        [defaults setBool:NO forKey:@"WebAutomaticQuoteSubstitutionEnabled"];
        
        // Disable automatic text replacement
        [defaults setBool:NO forKey:@"NSAutomaticTextReplacementEnabled"];
        [defaults setBool:NO forKey:@"WebAutomaticTextReplacementEnabled"];
        
        // Disable automatic spelling correction
        [defaults setBool:NO forKey:@"NSAutomaticSpellingCorrectionEnabled"];
        [defaults setBool:NO forKey:@"WebAutomaticSpellingCorrectionEnabled"];
        
        // Synchronize to ensure settings take effect immediately
        [defaults synchronize];
        
        NSLog(@"[Mitto] Automatic text substitutions disabled");
    }
}
