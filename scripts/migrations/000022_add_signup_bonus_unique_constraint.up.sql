-- Prevent duplicate signup bonuses at the database level.
-- Each user can only have one signup_bonus transaction.
CREATE UNIQUE INDEX idx_unique_signup_bonus 
    ON credit_transactions (user_id) 
    WHERE reference_id = 'signup_bonus' AND reference_type = 'system';
