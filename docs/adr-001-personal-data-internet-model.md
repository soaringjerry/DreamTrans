# ADR-001: The "Personal Data Internet" as the Core Architectural Metaphor

**Date**: 2025-07-06
**Status**: Accepted

## Context

As the DreamHub ecosystem grows, we need a robust, scalable, and intuitive architectural model to guide the development and interaction of its various dApps (Decentralized Applications) and the core PCAS (Personal Central AI System). Simple linear models like "pipelines" or "workflows" fail to capture the desired distributed, asynchronous, and event-driven nature of the system. We need a metaphor that embraces this complexity and provides clear guidance for future design decisions.

## Decision

We have decided to adopt the **"Personal Data Internet"** as the single, authoritative architectural metaphor for the entire PCAS ecosystem.

This model re-frames all components and interactions using the well-understood concepts of the global Internet and its core protocols, most notably BGP (Border Gateway Protocol).

### Core Concepts and Mappings

*   **dApps (e.g., DreamTrans, DreamNote)**: These are analogous to **Autonomous Systems (AS)** on the Internet. Each dApp is an independent, self-contained system with its own internal logic and responsibilities. It manages its own "internal network."

*   **PCAS (Personal Central AI System)**: This is the **Core Backbone Network** of our personal internet. Crucially, PCAS also runs the authoritative **BGP service**. It is responsible for discovering the capabilities of all connected dApps and routing information between them based on a user-defined policy.

*   **Events**: These are the **Data Packets** flowing through the network. Every event is a standardized data structure with:
    *   `event_type`: The "destination address," indicating the packet's intent.
    *   `source`: The "source address."
    *   `attributes`: Metadata tags, analogous to IP header options, providing additional context for routing and processing.

*   **`dapp.yaml` Manifest**: This is the mechanism by which an AS (dApp) **announces its routes to the BGP backbone (PCAS)**. It declares what `event_type`s it can provide (`provides`) and what it depends on (`requires`).

*   **`policy.yaml` (User-defined)**: This is the **Routing Policy Table** for the BGP engine within PCAS. It allows the user to create custom rules that determine how data packets (events) are routed. For example: `if event_type is X, then route to provider Y`.

*   **RAG (Retrieval-Augmented Generation)**: This is a **Value-Added Service** performed on the data path by the core backbone, analogous to Deep Packet Inspection (DPI) or a Content Delivery Network (CDN). When a packet flows through PCAS, the BGP engine can, based on policy, trigger an internal action to "deeply inspect" the packet, retrieve historical context from its "cache" (the user's knowledge graph), and dynamically "re-package" it with this new information before forwarding it to its final destination (e.g., an AI provider).

## Consequences

*   **Clear Separation of Concerns**: dApp developers only need to focus on their core logic and how to interact with the PCAS event bus. They do not need to know about other dApps.
*   **Extreme Extensibility**: New dApps can be "plugged into" the ecosystem simply by providing a `dapp.yaml` manifest and connecting to the PCAS backbone.
*   **User-centric Control**: The `policy.yaml` empowers the user to become the ultimate "network administrator" of their own personal data internet.
*   **Conceptual Integrity**: This single, powerful metaphor provides a unified language and mental model for all developers and stakeholders, ensuring that as the system evolves, it does so in a coherent and consistent manner. All future architectural decisions should be evaluated against their compatibility with this model.