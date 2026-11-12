# Checked owned results

Status: [reference]

An `own` result promises an owned value to the caller.
The checker validates each return.
The promise is part of a function type.

```fern
function pass[T](own value: T): own T {
  return value;
}

function apply[T](own value: T, step: (own T) => own T): own T {
  return step(value);
}
```

Annotate an owned-result lambda explicitly:

```fern
let step = (own value: string): own string => value;
```

The same result annotation applies to a local named function.

An ordinary result does not carry this promise.
An owned-result function can satisfy an ordinary-result function type.
The reverse assignment fails, even when one implementation returns an owned input.
Add the annotation to check and expose that guarantee.
A parameter keeps its ownership mode during assignment.

A borrowed resource parameter cannot become an owned result.
An aggregate or local alias cannot hide a resource loan.
Match payloads retain the loan from a borrowed scrutinee.
This also applies to nested matches and local aliases inside an arm.

A fresh aggregate can contain copied data parts when those parts cannot hold a handle.
A reusable closure borrows its captures.
This rule also applies to an outer owned parameter.
The closure cannot return such a capture as an owned result.

A returned callback retains the resource loans in its environment.
This includes callbacks passed through a factory, an owned callback parameter,
or a generic forwarding function.
Ownership of the callback value does not transfer a resource that its environment borrows.
An owned resource input instead transfers that resource into the returned callback.
A copied immutable result does not retain an environment loan.

A callback must finish its last use before a captured resource is consumed.
The check follows single-definition local bindings and inputs of resolved named factories.
A callback that returns another callback also retains these environment loans.
The check follows callback environments through records, tuples, arrays, and record updates.
A selected record or tuple field retains the loans of that component.
An unrelated callback field does not extend those loans.

Array elements share a conservative loan set.
Single-definition aggregate aliases preserve the selected component.
Each local declaration has a separate binding identity.
A shadowed name does not erase an outer callback loan.
Lambda parameters and local named functions use the same scope rule.

An index expression also retains loans from callbacks that compute its index.
Mutable bindings and resource paths inside aggregates still need a broader proof.

A fresh local can transfer after its capturing callbacks finish their last uses.
The checked proof requires a straight-line body with unmodified bindings.
Every callback reference must be a direct call before the owner's last read.
Aliases, stored or returned callbacks, and captures by another lambda prevent this proof.
Branches, loops, deferred uses, and assignments retain the conservative refusal.
The source must still satisfy the existing fresh-value ownership checks.

The caller can copy immutable data results.
This also applies to a call with an ordinary result type.
The call supplies an independent reference.
COW preserves the borrowed source.
A local with this result can transfer that reference at its last use.
This value-level copy rule does not strengthen a function type's result promise.

The checker validates the complete result type.
This rule includes maps when their keys and values contain only immutable data.
A cleanup type needs `@value` to retain this copy contract.
Cells and callbacks do not satisfy that immutable-data proof.

The result annotation does not change the ABI or add a copy.
The move checks still refuse repeated consumption.
WIT `own Resource` results retain their resource-handle contract.

Compiler-source adoption requires a published stage0 that accepts this syntax.
An updated checker built with the previous stage0 can validate programs that use it.

A return can transfer a fresh local at its last use.
It can also transfer an owned field.
A deferred cleanup cannot also consume the returned resource.
A cleanup of a separate field remains valid.

A checked result remains owned after a local binding.
Its nested resource fields can transfer at their last use.
Earlier scalar field copies do not retain a resource loan.

A returned record can replace an owned input field through a local alias.
The nested record can transfer a field of that alias into an owned argument.
The alias must remain unchanged and appear only inside that nested replacement.
The returned record must replace the complete input field that owns the alias.
Other aliases, callbacks, deferred cleanup, and repeated resource reads preserve the refusal.
An unrelated resource sibling can still prevent an owned result.
The `nested_owned_field_transfer` case checks the transfer and resource cleanup.
A tuple result can contain the same transfer, including within another tuple.
Other tuple parts must preserve the same owner and alias restrictions.
The `nested_owned_field_tuple` case checks transfer and cleanup on all three targets.

A numeric, boolean, or character COW array field copy does not retain a resource loan.
This rule includes nested arrays.
Resource arrays, mutable Cells, callbacks, and views keep their resource loans.

A resource alias, a deferred action, or a later read can prevent that transfer.
This rule includes checked callback results and local callback aliases.
An ordinary-result function type retains its ordinary result contract.
The `checked_owned_nested_results` case checks this guarantee.

A handle-free match payload can retain the contract of an unchanged owned input.
A completed borrowed read of that input does not remove this contract.
The `owned_field_match_data` case covers callback payloads.
Resource payloads still need the last-use transfer proof.
