export const inboxABI = [
  {
    type: 'function',
    inputs: [
      {
        name: 'order',
        internalType: 'struct IERC7683.OnchainCrossChainOrder',
        type: 'tuple',
        components: [
          { name: 'fillDeadline', internalType: 'uint32', type: 'uint32' },
          { name: 'orderDataType', internalType: 'bytes32', type: 'bytes32' },
          { name: 'orderData', internalType: 'bytes', type: 'bytes' },
        ],
      },
    ],
    name: 'open',
    outputs: [],
    stateMutability: 'payable',
  },
  {
    type: 'function',
    name: 'getOrder',
    inputs: [
      {
        name: 'id',
        type: 'bytes32',
        internalType: 'bytes32',
      },
    ],
    outputs: [
      {
        name: 'resolved',
        type: 'tuple',
        internalType: 'struct IERC7683.ResolvedCrossChainOrder',
        components: [
          {
            name: 'user',
            type: 'address',
            internalType: 'address',
          },
          {
            name: 'originChainId',
            type: 'uint256',
            internalType: 'uint256',
          },
          {
            name: 'openDeadline',
            type: 'uint32',
            internalType: 'uint32',
          },
          {
            name: 'fillDeadline',
            type: 'uint32',
            internalType: 'uint32',
          },
          {
            name: 'orderId',
            type: 'bytes32',
            internalType: 'bytes32',
          },
          {
            name: 'maxSpent',
            type: 'tuple[]',
            internalType: 'struct IERC7683.Output[]',
            components: [
              {
                name: 'token',
                type: 'bytes32',
                internalType: 'bytes32',
              },
              {
                name: 'amount',
                type: 'uint256',
                internalType: 'uint256',
              },
              {
                name: 'recipient',
                type: 'bytes32',
                internalType: 'bytes32',
              },
              {
                name: 'chainId',
                type: 'uint256',
                internalType: 'uint256',
              },
            ],
          },
          {
            name: 'minReceived',
            type: 'tuple[]',
            internalType: 'struct IERC7683.Output[]',
            components: [
              {
                name: 'token',
                type: 'bytes32',
                internalType: 'bytes32',
              },
              {
                name: 'amount',
                type: 'uint256',
                internalType: 'uint256',
              },
              {
                name: 'recipient',
                type: 'bytes32',
                internalType: 'bytes32',
              },
              {
                name: 'chainId',
                type: 'uint256',
                internalType: 'uint256',
              },
            ],
          },
          {
            name: 'fillInstructions',
            type: 'tuple[]',
            internalType: 'struct IERC7683.FillInstruction[]',
            components: [
              {
                name: 'destinationChainId',
                type: 'uint64',
                internalType: 'uint64',
              },
              {
                name: 'destinationSettler',
                type: 'bytes32',
                internalType: 'bytes32',
              },
              {
                name: 'originData',
                type: 'bytes',
                internalType: 'bytes',
              },
            ],
          },
        ],
      },
      {
        name: 'state',
        type: 'tuple',
        internalType: 'struct ISolverNetInbox.OrderState',
        components: [
          {
            name: 'status',
            type: 'uint8',
            internalType: 'enum ISolverNetInbox.Status',
          },
          {
            name: 'rejectReason',
            type: 'uint8',
            internalType: 'uint8',
          },
          {
            name: 'timestamp',
            type: 'uint32',
            internalType: 'uint32',
          },
          {
            name: 'updatedBy',
            type: 'address',
            internalType: 'address',
          },
        ],
      },
      {
        name: 'offset',
        type: 'uint248',
        internalType: 'uint248',
      },
    ],
    stateMutability: 'view',
  },
  {
    type: 'event',
    anonymous: false,
    inputs: [
      {
        name: 'orderId',
        internalType: 'bytes32',
        type: 'bytes32',
        indexed: true,
      },
      {
        name: 'resolvedOrder',
        internalType: 'struct IERC7683.ResolvedCrossChainOrder',
        type: 'tuple',
        components: [
          { name: 'user', internalType: 'address', type: 'address' },
          { name: 'originChainId', internalType: 'uint256', type: 'uint256' },
          { name: 'openDeadline', internalType: 'uint32', type: 'uint32' },
          { name: 'fillDeadline', internalType: 'uint32', type: 'uint32' },
          { name: 'orderId', internalType: 'bytes32', type: 'bytes32' },
          {
            name: 'maxSpent',
            internalType: 'struct IERC7683.Output[]',
            type: 'tuple[]',
            components: [
              { name: 'token', internalType: 'bytes32', type: 'bytes32' },
              { name: 'amount', internalType: 'uint256', type: 'uint256' },
              { name: 'recipient', internalType: 'bytes32', type: 'bytes32' },
              { name: 'chainId', internalType: 'uint256', type: 'uint256' },
            ],
          },
          {
            name: 'minReceived',
            internalType: 'struct IERC7683.Output[]',
            type: 'tuple[]',
            components: [
              { name: 'token', internalType: 'bytes32', type: 'bytes32' },
              { name: 'amount', internalType: 'uint256', type: 'uint256' },
              { name: 'recipient', internalType: 'bytes32', type: 'bytes32' },
              { name: 'chainId', internalType: 'uint256', type: 'uint256' },
            ],
          },
          {
            name: 'fillInstructions',
            internalType: 'struct IERC7683.FillInstruction[]',
            type: 'tuple[]',
            components: [
              {
                name: 'destinationChainId',
                internalType: 'uint64',
                type: 'uint64',
              },
              {
                name: 'destinationSettler',
                internalType: 'bytes32',
                type: 'bytes32',
              },
              { name: 'originData', internalType: 'bytes', type: 'bytes' },
            ],
          },
        ],
        indexed: false,
      },
    ],
    name: 'Open',
  },
  {
    type: 'event',
    name: 'Rejected',
    inputs: [
      {
        name: 'id',
        type: 'bytes32',
        indexed: true,
        internalType: 'bytes32',
      },
      {
        name: 'by',
        type: 'address',
        indexed: true,
        internalType: 'address',
      },
      {
        name: 'reason',
        type: 'uint8',
        indexed: true,
        internalType: 'uint8',
      },
    ],
    anonymous: false,
  },
] as const

export const outboxABI = [
  {
    type: 'function',
    inputs: [
      { name: 'orderId', internalType: 'bytes32', type: 'bytes32' },
      { name: 'originData', internalType: 'bytes', type: 'bytes' },
    ],
    name: 'didFill',
    outputs: [{ name: '', internalType: 'bool', type: 'bool' }],
    stateMutability: 'view',
  },
  {
    anonymous: false,
    inputs: [
      {
        name: 'orderId',
        type: 'bytes32',
        indexed: true,
        internalType: 'bytes32',
      },
      {
        name: 'fillHash',
        type: 'bytes32',
        indexed: true,
        internalType: 'bytes32',
      },
      {
        name: 'filledBy',
        type: 'address',
        indexed: true,
        internalType: 'address',
      },
    ],
    name: 'Filled',
    type: 'event',
  },
] as const

export const executorABI = [
  {
    type: 'function',
    inputs: [
      { name: 'token', internalType: 'address', type: 'address' },
      { name: 'to', internalType: 'address', type: 'address' },
      { name: 'target', internalType: 'address', type: 'address' },
      { name: 'data', internalType: 'bytes', type: 'bytes' },
    ],
    name: 'executeAndTransfer',
    outputs: [],
    stateMutability: 'payable',
  },
  { type: 'error', inputs: [], name: 'CallFailed' },
] as const

export const fillOriginDataAbi = {
  type: 'tuple',
  components: [
    { name: 'srcChainId', type: 'uint64' },
    { name: 'destChainId', type: 'uint64' },
    { name: 'fillDeadline', type: 'uint32' },
    {
      name: 'calls',
      type: 'tuple[]',
      components: [
        { name: 'target', type: 'address' },
        { name: 'selector', type: 'bytes4' },
        { name: 'value', type: 'uint256' },
        { name: 'params', type: 'bytes' },
      ],
    },
    {
      name: 'expenses',
      type: 'tuple[]',
      components: [
        { name: 'spender', type: 'address' },
        { name: 'token', type: 'address' },
        { name: 'amount', type: 'uint96' },
      ],
    },
  ],
} as const
