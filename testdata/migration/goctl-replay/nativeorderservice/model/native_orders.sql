CREATE TABLE `native_order`
(
    `id` bigint(20) NOT NULL AUTO_INCREMENT,
    `buyer_name` varchar(128) NOT NULL DEFAULT '' COMMENT 'buyer name',
    `amount_cents` bigint(20) NOT NULL DEFAULT 0 COMMENT 'amount in cents',
    `status` varchar(32) NOT NULL DEFAULT '' COMMENT 'order status',
    `created_at` timestamp NULL DEFAULT CURRENT_TIMESTAMP,
    `updated_at` timestamp NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uk_native_order_buyer_status` (`buyer_name`, `status`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
