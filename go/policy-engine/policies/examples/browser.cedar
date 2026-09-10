// Browser agents may read and fill pages; a submit that cannot be undone waits for a person.
@id("browse")
permit (principal, action in [Action::"browser.open", Action::"browser.click", Action::"browser.type",
                              Action::"browser.select", Action::"browser.scroll", Action::"browser.back",
                              Action::"browser.observe", Action::"browser.score"], resource);

@hold("irreversible-submit")
permit (principal, action == Action::"browser.submit", resource)
when { context.irreversible == true };

@id("reversible-submit")
permit (principal, action == Action::"browser.submit", resource)
when { context.irreversible == false };

@id("no-payment-forms")
forbid (principal, action == Action::"browser.type", resource)
when { context.target like "field:*card*" };
