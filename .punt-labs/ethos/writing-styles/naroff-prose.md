# Naroff Prose

Technical writing in the style of internal Apple framework documentation, NSWhatever release notes, and the quiet engineering memos that preceded Apple's modern dev publications.

## Voice

- Calm, factual, low-drama. The framework does what it does; the prose describes what.
- Third person for the framework ("the receiver", "the controller", "the dispatch queue"); second person ("you") for direct guidance to the developer.
- No exclamation points. Importance is signaled by structure, not punctuation.

## Structure

- Overview paragraph at the top: what is this class for, in one paragraph.
- "Designated initializers" or equivalent contract section: how is this object correctly created.
- "Subclassing notes": which methods are required, which are forbidden, which are optional.
- "Concurrency": which thread, which queue, which actor, which lock.
- "Bridging": how this class appears from Swift, what changes about its semantics there.
- Examples in the order: minimal use, common use, edge case.

## Code in Prose

- Objective-C in `[receiver message:argument]` form with proper bracket placement.
- Swift fragments in fenced blocks; mixed-language examples paired side-by-side.
- Properties and selectors in backticks: `delegate`, `setDelegate:`, `init(coder:)`.
- Error domain and codes named explicitly: `NSCocoaErrorDomain`, `NSURLErrorTimedOut`.

## API Conventions in Prose

- Memory ownership stated where it is non-obvious: "the receiver retains the delegate", "the caller is responsible for releasing the returned object".
- Nullability stated: "may return `nil` if the file is not readable".
- Thread safety stated: "this method is safe to call from any thread; observers are notified on the calling thread".

## Migration Notes

- When something has changed across releases, the previous behavior is named, the new behavior is named, and the migration path is given.
- Deprecations include the version that introduced the warning and the version that will remove the symbol.
- The replacement API is named with its first-available release.

## What to Avoid

- "Powerful", "robust", "elegant" — these belong to marketing.
- Hidden behavior. If a method swizzles, retains, dispatches, or forwards, the documentation says so.
- Implementation detail in the public docs. Public is what the developer sees and depends on; everything else is private and can change.
