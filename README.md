# User Microservice

This repository contains the User Microservice, which provides various endpoints for managing users, roles, and organizations. This service is built using Go and the Gin framework.

## Features

- User authentication and management
- Role management
- Organization management

## API Endpoints


### User Management

- **`POST /user/signup`**: 
  - **Description**: Registers a new user in the system by collecting user details. This endpoint is intended for an admin of an organization to create new users within that organization.
- **`POST /user/confirm`**: 
  - **Description**: Confirms user registration, typically used to verify the email address after signup.

- **`POST /user/resend-confirm`**: 
  - **Description**: Resends the confirmation email to the user, useful if the user did not receive it initially.

- **`POST /user/login`**: 
  - **Description**: Authenticates the user and returns a token for subsequent requests.

- **`POST /user/reset-password/init`**: 
  - **Description**: Initiates the password reset process, sending an email with reset instructions.

- **`POST /user/reset-password/confirm`**: 
  - **Description**: Confirms the password reset by updating the user’s password.

- **`POST /user/refresh-token`**: 
  - **Description**: Refreshes the authentication token for a logged-in user.

- **`PUT /user/update-user/:prefix`**: 
  - **Description**: Updates the user's information. The `:prefix` parameter is used to identify the user by their external ID.

- **`GET /user/`**: 
  - **Description**: Retrieves a paginated list of all users in the system.

- **`DELETE /user/:prefix`**: 
  - **Description**: Deletes a user identified by the `:prefix` parameter.

- **`POST /user/`**: 
  - **Description**: Adds a new user to the system. This is similar to the signup endpoint but is used for adding users that belong to an organization.
  - To use this endpoint, the organization ID must be included in the request. The user can only be added if the organization has an active subscription and if the employee limit for the current subscription plan has not been reached.

- **`GET /user/get-user-by-id/:prefix`**: 
  - **Description**: Retrieves user information by their unique MongoDB ID.

- **`GET /user/get-user/:prefix`**: 
  - **Description**: Retrieves user information using an external ID.

### Role Management

- **`GET /role/:prefix`**: 
  - **Description**: Retrieves a role by its external ID.

- **`GET /role/`**: 
  - **Description**: Retrieves a list of all roles in the system.

- **`POST /role/`**: 
  - **Description**: Adds a new role to the system.

- **`PUT /role/:prefix`**: 
  - **Description**: Updates an existing role identified by its external ID.

- **`DELETE /role/:prefix`**: 
  - **Description**: Deletes a role identified by its external ID.

### Organization Management

- **`POST /organization/`**: 
  - **Description**: Adds a new organization to the system.

- **`PUT /organization/:prefix`**: 
  - **Description**: Updates an existing organization identified by its external ID.

- **`DELETE /organization/:prefix`**: 
  - **Description**: Deletes an organization identified by its external ID.

- **`GET /organization/:prefix`**: 
  - **Description**: Retrieves organization details by its external ID.

- **`GET /organization/`**: 
  - **Description**: Retrieves a list of all organizations in the system.


