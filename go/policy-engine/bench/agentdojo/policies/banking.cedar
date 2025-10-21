// The AgentDojo banking suite under policy: read freely, pay only known payees,
// hold anything that changes account credentials.
@id("read")
permit (principal, action in [Action::"get_balance", Action::"get_most_recent_transactions",
                              Action::"get_scheduled_transactions", Action::"read_file",
                              Action::"get_iban", Action::"get_user_info"], resource);

@id("pay-known-payee")
permit (principal, action in [Action::"send_money", Action::"schedule_transaction",
                              Action::"update_scheduled_transaction"], resource)
when { context.recipient in context.known_payees };

@hold("credential-change")
permit (principal, action in [Action::"update_password", Action::"update_user_info"], resource);
