Living record of refactoring decisions

There are one (or two) files remaining in the worker package that depend on the resource package.
We think that if we can decouple this, then the import cycle should stop and we should be back to a compilable state.